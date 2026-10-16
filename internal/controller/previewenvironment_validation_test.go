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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

// These tests exercise the OpenAPI validation generated from the markers in
// previewenvironment_types.go. Every create is a server-side dry run: the API
// server fully validates the object but never persists it, so no cleanup is needed.
var _ = Describe("PreviewEnvironment API validation", func() {
	const namespace = "default"

	validSpec := func() previewv1alpha1.PreviewEnvironmentSpec {
		return previewv1alpha1.PreviewEnvironmentSpec{
			Repository: "seppys/pr-env",
			Image:      "nginx:1.27",
			Port:       8080,
		}
	}

	newPreviewEnvironment := func(spec previewv1alpha1.PreviewEnvironmentSpec) *previewv1alpha1.PreviewEnvironment {
		return &previewv1alpha1.PreviewEnvironment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "validation-test",
				Namespace: namespace,
			},
			Spec: spec,
		}
	}

	dryRunCreate := func(obj client.Object) error {
		return k8sClient.Create(ctx, obj, client.DryRunAll)
	}

	It("should accept a valid spec", func() {
		Expect(dryRunCreate(newPreviewEnvironment(validSpec()))).To(Succeed())
	})

	DescribeTable("port",
		func(port int32, shouldSucceed bool) {
			spec := validSpec()
			spec.Port = port

			err := dryRunCreate(newPreviewEnvironment(spec))
			if shouldSucceed {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got: %v", err)
			Expect(err.Error()).To(ContainSubstring("spec.port"))
		},
		Entry("accepts the lower bound", int32(1), true),
		Entry("accepts the upper bound", int32(65535), true),
		Entry("rejects zero", int32(0), false),
		Entry("rejects negative values", int32(-1), false),
		Entry("rejects values above 65535", int32(65536), false),
	)

	// The typed struct always serializes every field (none use omitempty), so it can't
	// express a missing field. An unstructured object lets us drop the key entirely.
	DescribeTable("required fields",
		func(field string) {
			spec := map[string]any{
				"repository": "seppys/pr-env",
				"image":      "nginx:1.27",
				"port":       int64(8080),
			}
			delete(spec, field)

			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": previewv1alpha1.GroupVersion.String(),
				"kind":       "PreviewEnvironment",
				"metadata": map[string]any{
					"name":      "validation-test",
					"namespace": namespace,
				},
				"spec": spec,
			}}

			err := dryRunCreate(obj)
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got: %v", err)
			Expect(err.Error()).To(ContainSubstring("spec." + field))
			Expect(err.Error()).To(ContainSubstring("Required value"))
		},
		Entry("rejects a missing repository", "repository"),
		Entry("rejects a missing image", "image"),
		Entry("rejects a missing port", "port"),
	)
})
