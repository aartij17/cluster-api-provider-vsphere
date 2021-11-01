// Copyright (c) 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package serviceaccount_test

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	tkgv1 "gitlab.eng.vmware.com/core-build/guest-cluster-controller/apis/run.tanzu/v1alpha2"
	"gitlab.eng.vmware.com/core-build/guest-cluster-controller/controllers/serviceaccount"
	"gitlab.eng.vmware.com/core-build/guest-cluster-controller/test/builder"
)

// suite is used for unit and integration testing this controller.
var suite = builder.NewTestSuiteForController(serviceaccount.AddToManager, serviceaccount.NewReconciler)

func TestController(t *testing.T) {
	suite.Register(t, "ProviderServiceaccount controller suite", intgTests, unitTests)
}

var _ = BeforeSuite(suite.BeforeSuite)

var _ = AfterSuite(suite.AfterSuite)

const (
	testNS                     = "test-namespace"
	testProviderSvcAccountName = "test-pvcsi"
	testTargetNS               = "test-pvcsi-system"
	testTargetSecret           = "test-pvcsi-secret" // nolint:gosec
	testSvcAccountName         = testProviderSvcAccountName
	testSvcAccountSecretName   = testSvcAccountName + "-token-abcdef"
	testRoleName               = testProviderSvcAccountName
	testRoleBindingName        = testProviderSvcAccountName
	testSystemSvcAcctNs        = "test-system-svc-acct-namespace"
	testSystemSvcAcctCM        = "test-system-svc-acct-cm"

	testSecretToken = "ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklp" // nolint:gosec
)

var (
	truePointer = true
)

func createTestResource(ctx context.Context, ctrlClient client.Client, obj runtime.Object) {
	Expect(ctrlClient.Create(ctx, obj)).To(Succeed())
}

func deleteTestResource(ctx context.Context, ctrlClient client.Client, obj runtime.Object) {
	Expect(ctrlClient.Delete(ctx, obj)).To(Succeed())
}

func createTestProviderSvcAccountWithInvalidRef(ctx context.Context, ctrlClient client.Client, namespace string, tanzukubernetescluster *tkgv1.TanzuKubernetesCluster) {
	pSvcAccount := getTestProviderServiceAccount(namespace, testProviderSvcAccountName, tanzukubernetescluster)
	pSvcAccount.Spec.Ref = &corev1.ObjectReference{}
	createTestResource(ctx, ctrlClient, pSvcAccount)
}

func createTargetSecretWithInvalidToken(ctx context.Context, guestClient client.Client) {
	secret := getTestTargetSecretWithInvalidToken()
	Expect(guestClient.Create(ctx, secret)).To(Succeed())
}

func assertEventuallyExistsInNamespace(ctx context.Context, c client.Client, namespace, name string, obj runtime.Object) {
	EventuallyWithOffset(2, func() error {
		key := client.ObjectKey{Namespace: namespace, Name: name}
		return c.Get(ctx, key, obj)
	}).Should(Succeed())
}

func assertNoEntities(ctx context.Context, ctrlClient client.Client, namespace string) {
	Consistently(func() int {
		var serviceAccountList corev1.ServiceAccountList
		err := ctrlClient.List(ctx, &serviceAccountList, client.InNamespace(namespace))
		Expect(err).ShouldNot(HaveOccurred())
		return len(serviceAccountList.Items)
	}, time.Second*3).Should(Equal(0))

	Consistently(func() int {
		var roleList rbacv1.RoleList
		err := ctrlClient.List(ctx, &roleList, client.InNamespace(namespace))
		Expect(err).ShouldNot(HaveOccurred())
		return len(roleList.Items)
	}, time.Second*3).Should(Equal(0))

	Consistently(func() int {
		var roleBindingList rbacv1.RoleBindingList
		err := ctrlClient.List(ctx, &roleBindingList, client.InNamespace(namespace))
		Expect(err).ShouldNot(HaveOccurred())
		return len(roleBindingList.Items)
	}, time.Second*3).Should(Equal(0))
}

func assertServiceAccountAndUpdateSecret(ctx context.Context, ctrlClient client.Client, namespace, name string) {
	svcAccount := &corev1.ServiceAccount{}
	assertEventuallyExistsInNamespace(ctx, ctrlClient, namespace, name, svcAccount)
	// Update the service account with a prototype secret
	secret := getTestSvcAccountSecret(namespace, testSvcAccountSecretName)
	Expect(ctrlClient.Create(ctx, secret)).To(Succeed())
	svcAccount.Secrets = []corev1.ObjectReference{
		{
			Name: testSvcAccountSecretName,
		},
	}
	Expect(ctrlClient.Update(ctx, svcAccount)).To(Succeed())
}

func assertTargetSecret(ctx context.Context, guestClient client.Client, namespace, name string) {
	secret := &corev1.Secret{}
	assertEventuallyExistsInNamespace(ctx, guestClient, namespace, name, secret)
	EventuallyWithOffset(2, func() []byte {
		key := client.ObjectKey{Namespace: namespace, Name: name}
		Expect(guestClient.Get(ctx, key, secret)).Should(Succeed())
		return secret.Data["token"]
	}).Should(Equal([]byte(testSecretToken)))
}

func assertTargetNamespace(ctx context.Context, guestClient client.Client, namespaceName string, isExist bool) {
	namespace := &corev1.Namespace{}
	err := guestClient.Get(ctx, client.ObjectKey{Name: namespaceName}, namespace)
	if isExist {
		Expect(err).NotTo(HaveOccurred())
	} else {
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}
}

func assertRoleWithGetPVC(ctx context.Context, ctrlClient client.Client, namespace, name string) {
	var roleList rbacv1.RoleList
	opts := &client.ListOptions{
		Namespace: namespace,
	}
	err := ctrlClient.List(ctx, &roleList, opts)
	Expect(err).ShouldNot(HaveOccurred())
	Expect(len(roleList.Items)).To(Equal(1))
	Expect(roleList.Items[0].Name).To(Equal(name))
	Expect(roleList.Items[0].Rules).To(Equal([]rbacv1.PolicyRule{
		{
			Verbs:     []string{"get"},
			APIGroups: []string{""},
			Resources: []string{"persistentvolumeclaims"},
		},
	}))
}

func assertRoleBinding(ctx context.Context, ctrlClient client.Client, namespace, name string) {
	var roleBindingList rbacv1.RoleBindingList
	opts := &client.ListOptions{
		Namespace: namespace,
	}
	err := ctrlClient.List(context.TODO(), &roleBindingList, opts)
	Expect(err).ShouldNot(HaveOccurred())
	Expect(len(roleBindingList.Items)).To(Equal(1))
	Expect(roleBindingList.Items[0].Name).To(Equal(name))
	Expect(roleBindingList.Items[0].RoleRef).To(Equal(rbacv1.RoleRef{
		Name:     testRoleName,
		Kind:     "Role",
		APIGroup: rbacv1.GroupName,
	}))
}

func assertProviderServiceAccountsCondition(tkc *tkgv1.TanzuKubernetesCluster, status corev1.ConditionStatus,
	message string, reason string, severity clusterv1.ConditionSeverity) {
	c := conditions.Get(tkc, tkgv1.ProviderServiceAccountsReadyCondition)
	Expect(c).NotTo(BeNil())
	Expect(c.Status).To(Equal(status))
	Expect(c.Reason).To(Equal(reason))
	Expect(c.Severity).To(Equal(severity))
	if message == "" {
		Expect(c.Message).To(BeEmpty())
	} else {
		Expect(strings.Contains(c.Message, message)).To(BeTrue(), "expect condition message contains: %s, actual: %s", message, c.Message)
	}
}

func getTestTargetSecretWithInvalidToken() *corev1.Secret {
	secret := getTestTargetSecretWithValidToken()
	secret.Data["token"] = []byte("invalid-token")
	return secret
}

func getTestTargetSecretWithValidToken() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTargetSecret,
			Namespace: testTargetNS,
		},
		Data: map[string][]byte{
			"token": []byte(testSecretToken),
		},
	}
}

func getTestProviderServiceAccount(namespace, name string, tanzukubernetescluster *tkgv1.TanzuKubernetesCluster) *tkgv1.ProviderServiceAccount {
	pSvcAccount := &tkgv1.ProviderServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: tkgv1.ProviderServiceAccountSpec{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"persistentvolumeclaims"},
				},
			},
			TargetNamespace:  testTargetNS,
			TargetSecretName: testTargetSecret,
		}}
	if tanzukubernetescluster != nil {
		pSvcAccount.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: tkgv1.GroupVersion.String(),
				Kind:       "TanzuKubernetesCluster",
				Name:       tanzukubernetescluster.Name,
				UID:        tanzukubernetescluster.UID,
				Controller: &truePointer,
			},
		}
		pSvcAccount.Spec.Ref = &corev1.ObjectReference{
			Name: tanzukubernetescluster.Name,
		}
	}
	return pSvcAccount
}

func getSystemServiceAccountsConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string]string{
			"system-account-1": "true",
			"system-account-2": "true",
		},
	}
}

func getTestSvcAccountSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"token": []byte(testSecretToken),
		},
	}
}

func getTestRoleWithGetPod(namespace, name string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{""},
				Resources: []string{"pods"},
			},
		},
	}
}

func getTestRoleBindingWithInvalidRoleRef(namespace, name string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespace,
			Namespace: name,
		},
		RoleRef: rbacv1.RoleRef{
			Name:     "invalid-role-ref",
			Kind:     "Role",
			APIGroup: rbacv1.GroupName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      testSvcAccountName,
				Namespace: namespace},
		},
	}
}
