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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

var _ = Describe("PreviewEnvironment Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		previewenvironment := &previewv1alpha1.PreviewEnvironment{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind PreviewEnvironment")
			err := k8sClient.Get(ctx, typeNamespacedName, previewenvironment)
			if err != nil && errors.IsNotFound(err) {
				resource := &previewv1alpha1.PreviewEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: previewv1alpha1.PreviewEnvironmentSpec{
						Repository: testRepository,
						Image:      testImage,
						Port:       8080,
						PRNumber:   testPRNumber,
						CommitSHA:  testCommitSHA,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &previewv1alpha1.PreviewEnvironment{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance PreviewEnvironment")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &PreviewEnvironmentReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				PreviewDomain: "preview.local",
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.

			By("Routing <prNumber>-<name>.<preview domain> to the Service")
			ing := &networkingv1.Ingress{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, ing)).To(Succeed())
			Expect(ing.Spec.Rules).To(HaveLen(1))
			Expect(ing.Spec.Rules[0].Host).To(Equal(fmt.Sprintf("%d-%s.preview.local", testPRNumber, resourceName)))
			paths := ing.Spec.Rules[0].HTTP.Paths
			Expect(paths).To(HaveLen(1))
			Expect(paths[0].Path).To(Equal("/"))
			Expect(paths[0].Backend.Service.Name).To(Equal(resourceName))
			Expect(paths[0].Backend.Service.Port.Name).To(Equal("http"))

			resource := &previewv1alpha1.PreviewEnvironment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(metav1.IsControlledBy(ing, resource)).To(BeTrue())
		})
	})
})
