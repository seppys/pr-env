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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

var _ = Describe("PreviewEnvironment Controller failures", func() {
	const (
		name      = "collision-test"
		namespace = "default"
	)
	key := types.NamespacedName{Name: name, Namespace: namespace}
	objectMeta := metav1.ObjectMeta{Name: name, Namespace: namespace}

	AfterEach(func() {
		// envtest has no garbage collector, so owned children must be deleted explicitly too.
		for _, obj := range []client.Object{
			&previewv1alpha1.PreviewEnvironment{ObjectMeta: objectMeta},
			&appsv1.Deployment{ObjectMeta: objectMeta},
			&corev1.Service{ObjectMeta: objectMeta},
			&networkingv1.Ingress{ObjectMeta: objectMeta},
		} {
			err := k8sClient.Delete(ctx, obj)
			Expect(apierrors.IsNotFound(err) || err == nil).To(BeTrue(), "deleting %T: %v", obj, err)
		}
	})

	It("reports ServiceReconcileFailed when a Service with the same name is controlled by someone else", func() {
		By("creating a Service that another controller already owns")
		Expect(k8sClient.Create(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "someone-else",
					UID:        "00000000-0000-0000-0000-000000000001",
					Controller: new(true),
				}},
			},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
		})).To(Succeed())

		By("creating the PreviewEnvironment")
		Expect(k8sClient.Create(ctx, &previewv1alpha1.PreviewEnvironment{
			ObjectMeta: objectMeta,
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
		var alreadyOwned *controllerutil.AlreadyOwnedError
		Expect(errors.As(err, &alreadyOwned)).To(BeTrue(), "expected an AlreadyOwnedError, got: %v", err)

		By("recording the failure on the Ready condition")
		env := &previewv1alpha1.PreviewEnvironment{}
		Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
		cond := meta.FindStatusCondition(env.Status.Conditions, conditionTypeReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(ReasonServiceReconcileFailed))
		Expect(cond.Message).To(ContainSubstring("reconciling Service"))
	})
})
