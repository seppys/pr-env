/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

// removeFinalizerAndDelete deletes a PreviewEnvironment in tests. No controller runs in
// envtest to remove the finalizer, so the test removes it first; otherwise the object
// would stay Terminating and collide with the next test that uses the same name.
func removeFinalizerAndDelete(ctx context.Context, key types.NamespacedName) {
	env := &previewv1alpha1.PreviewEnvironment{}
	err := k8sClient.Get(ctx, key, env)
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	if controllerutil.RemoveFinalizer(env, cleanupFinalizer) {
		Expect(k8sClient.Update(ctx, env)).To(Succeed())
	}
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, env))).To(Succeed())
}

var _ = Describe("PreviewEnvironment Controller", func() {
	ctx := context.Background()

	newReconciler := func() *PreviewEnvironmentReconciler {
		return &PreviewEnvironmentReconciler{
			Client:        k8sClient,
			Scheme:        k8sClient.Scheme(),
			PreviewDomain: "preview.local",
		}
	}

	createEnvironment := func(key types.NamespacedName) {
		Expect(k8sClient.Create(ctx, &previewv1alpha1.PreviewEnvironment{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: previewv1alpha1.PreviewEnvironmentSpec{
				Repository: testRepository,
				Image:      testImage,
				Port:       8080,
				PRNumber:   testPRNumber,
				CommitSHA:  testCommitSHA,
			},
		})).To(Succeed())
	}

	// expectOwnerLabels checks obj points back at the PreviewEnvironment identified by key.
	expectOwnerLabels := func(obj metav1.Object, key types.NamespacedName) {
		Expect(obj.GetLabels()).To(HaveKeyWithValue(ownerNameLabel, key.Name))
		Expect(obj.GetLabels()).To(HaveKeyWithValue(ownerNamespaceLabel, key.Namespace))
	}

	Context("When reconciling a resource", func() {
		key := types.NamespacedName{Name: "test-resource", Namespace: testNamespace}

		BeforeEach(func() { createEnvironment(key) })
		AfterEach(func() { removeFinalizerAndDelete(ctx, key) })

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			By("Adding the cleanup finalizer")
			env := &previewv1alpha1.PreviewEnvironment{}
			Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(env, cleanupFinalizer)).To(BeTrue())

			By("Creating a namespace for the preview, labelled with its owner")
			ns := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: key.Name}, ns)).To(Succeed())
			expectOwnerLabels(ns, key)

			By("Routing <prNumber>-<name>.<preview domain> to the Service, inside the preview namespace")
			ing := &networkingv1.Ingress{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: ns.Name}, ing)).To(Succeed())
			expectOwnerLabels(ing, key)
			Expect(ing.Spec.Rules).To(HaveLen(1))
			Expect(ing.Spec.Rules[0].Host).To(Equal(fmt.Sprintf("%d-%s.preview.local", testPRNumber, key.Name)))
			paths := ing.Spec.Rules[0].HTTP.Paths
			Expect(paths).To(HaveLen(1))
			Expect(paths[0].Path).To(Equal("/"))
			Expect(paths[0].Backend.Service.Name).To(Equal(key.Name))
			Expect(paths[0].Backend.Service.Port.Name).To(Equal("http"))
		})
	})

	Context("When deleting a resource", func() {
		// Its own name: envtest has no namespace controller, so the namespace this test
		// deletes stays Terminating and can't be reused by another test.
		key := types.NamespacedName{Name: "deletion-test", Namespace: testNamespace}

		BeforeEach(func() { createEnvironment(key) })
		AfterEach(func() { removeFinalizerAndDelete(ctx, key) })

		It("deletes the preview namespace and keeps the finalizer until it is gone", func() {
			reconciler := newReconciler()
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			By("Deleting the PreviewEnvironment")
			env := &previewv1alpha1.PreviewEnvironment{}
			Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
			Expect(k8sClient.Delete(ctx, env)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should poll until the namespace is gone")

			By("Requesting deletion of the namespace")
			ns := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: key.Name}, ns)).To(Succeed())
			Expect(ns.DeletionTimestamp.IsZero()).To(BeFalse())

			By("Keeping the finalizer while the namespace is terminating")
			Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(env, cleanupFinalizer)).To(BeTrue())
		})
	})
})

var _ = Describe("environmentForObject", func() {
	It("maps a labelled child to its PreviewEnvironment", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			ownerNameLabel:      "demo",
			ownerNamespaceLabel: testNamespace,
		}}}
		Expect(environmentForObject(context.Background(), ns)).To(Equal([]reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: "demo", Namespace: testNamespace}},
		}))
	})

	It("ignores objects without owner labels", func() {
		Expect(environmentForObject(context.Background(), &corev1.Namespace{})).To(BeEmpty())
	})
})
