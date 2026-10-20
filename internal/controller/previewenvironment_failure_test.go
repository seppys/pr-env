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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

var _ = Describe("PreviewEnvironment Controller failures", func() {
	// The PreviewEnvironment and the pre-existing namespace share a name, which is the collision.
	const name = "collision-test"
	key := types.NamespacedName{Name: name, Namespace: testNamespace}

	AfterEach(func() { removeFinalizerAndDelete(ctx, key) })

	It("refuses to adopt a namespace it didn't create and reports NamespaceReconcileFailed", func() {
		By("creating a namespace without owner labels")
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed())

		By("creating the PreviewEnvironment")
		Expect(k8sClient.Create(ctx, &previewv1alpha1.PreviewEnvironment{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: previewv1alpha1.PreviewEnvironmentSpec{
				Repository: testRepository,
				Image:      testImage,
				Port:       80,
				PRNumber:   testPRNumber,
				CommitSHA:  testCommitSHA,
			},
		})).To(Succeed())

		By("reconciling")
		reconciler := &PreviewEnvironmentReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), PreviewDomain: "preview.local",
		}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})

		By("returning the error so controller-runtime retries")
		Expect(err).To(MatchError(ContainSubstring("not managed by this PreviewEnvironment")))

		By("recording the failure on the Ready condition")
		env := &previewv1alpha1.PreviewEnvironment{}
		Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
		cond := meta.FindStatusCondition(env.Status.Conditions, conditionTypeReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(ReasonNamespaceReconcileFailed))

		By("leaving the foreign namespace alone when the PreviewEnvironment is deleted")
		Expect(k8sClient.Delete(ctx, env)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp.IsZero()).To(BeTrue(), "the foreign namespace must not be deleted")
		Expect(k8sClient.Get(ctx, key, env)).NotTo(Succeed(), "the finalizer should be removed so deletion completes")
	})
})
