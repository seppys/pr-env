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

package githubwebhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
)

var _ = Describe("Handler", func() {
	const namespace = "default"

	var (
		fetcher *fakeFetcher
		handler *Handler
	)

	BeforeEach(func() {
		fetcher = &fakeFetcher{cfg: repoConfig{Image: testImageTemplate, Port: 8080}}
		handler = &Handler{
			secret:  testSecret,
			service: &service{client: k8sClient, namespace: namespace, configs: fetcher},
		}
	})

	// sendWithSignature posts body as a GitHub webhook delivery with the given signature header.
	sendWithSignature := func(eventType string, body []byte, signature string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", eventType)
		req.Header.Set("X-GitHub-Delivery", "test-delivery")
		if signature != "" {
			req.Header.Set(signatureHeader, signature)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// send posts body as a correctly signed GitHub webhook delivery of the given event type.
	send := func(eventType string, body []byte) *httptest.ResponseRecorder {
		return sendWithSignature(eventType, body, sign(testSecret, body))
	}

	sendEvent := func(e pullRequestEvent) *httptest.ResponseRecorder {
		body, err := json.Marshal(e)
		Expect(err).NotTo(HaveOccurred())
		return send("pull_request", body)
	}

	// keyFor is where the handler stores the PreviewEnvironment for a PR. Each test uses its own
	// PR number, and the object is deleted afterwards, so tests don't see each other's objects.
	keyFor := func(prNumber int32) types.NamespacedName {
		key := types.NamespacedName{Name: environmentName(testRepo, prNumber), Namespace: namespace}
		DeferCleanup(func() {
			env := &previewv1alpha1.PreviewEnvironment{}
			env.Name, env.Namespace = key.Name, key.Namespace
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, env))).To(Succeed())
		})
		return key
	}

	expectNoEnvironment := func(key types.NamespacedName) {
		err := k8sClient.Get(ctx, key, &previewv1alpha1.PreviewEnvironment{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected no PreviewEnvironment, got: %v", err)
	}

	Describe("signature", func() {
		It("rejects an unsigned delivery without acting on it", func() {
			key := keyFor(501)
			body, err := json.Marshal(newEvent(actionOpened, 501, testHeadSHA))
			Expect(err).NotTo(HaveOccurred())

			Expect(sendWithSignature("pull_request", body, "").Code).To(Equal(http.StatusUnauthorized))
			expectNoEnvironment(key)
		})

		It("rejects a delivery signed with another secret", func() {
			key := keyFor(502)
			body, err := json.Marshal(newEvent(actionOpened, 502, testHeadSHA))
			Expect(err).NotTo(HaveOccurred())

			rec := sendWithSignature("pull_request", body, sign([]byte("attacker-secret"), body))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			expectNoEnvironment(key)
		})

		It("rejects a body modified after signing", func() {
			key := keyFor(503)
			signed, err := json.Marshal(newEvent(actionOpened, 503, testHeadSHA))
			Expect(err).NotTo(HaveOccurred())
			tampered, err := json.Marshal(newEvent(actionOpened, 503, "4444444444444444444444444444444444444444"))
			Expect(err).NotTo(HaveOccurred())

			Expect(sendWithSignature("pull_request", tampered, sign(testSecret, signed)).Code).
				To(Equal(http.StatusUnauthorized))
			expectNoEnvironment(key)
		})

		It("checks the signature before routing, so unsigned pings are rejected too", func() {
			Expect(sendWithSignature("ping", []byte(`{}`), "").Code).To(Equal(http.StatusUnauthorized))
		})
	})

	Describe("routing", func() {
		It("answers GitHub's ping", func() {
			Expect(send("ping", []byte(`{"zen":"Keep it logically awesome."}`)).Code).To(Equal(http.StatusOK))
		})

		It("ignores events other than pull_request", func() {
			Expect(send("push", []byte(`{}`)).Code).To(Equal(http.StatusNoContent))
		})

		It("ignores pull_request actions it doesn't handle", func() {
			key := keyFor(101)
			Expect(sendEvent(newEvent("labeled", 101, testHeadSHA)).Code).To(Equal(http.StatusNoContent))
			expectNoEnvironment(key)
		})
	})

	Describe("bad requests", func() {
		It("rejects malformed JSON", func() {
			Expect(send("pull_request", []byte(`{not json`)).Code).To(Equal(http.StatusBadRequest))
		})

		It("rejects a payload missing required fields", func() {
			e := newEvent(actionOpened, 102, testHeadSHA)
			e.PullRequest.Head.SHA = ""
			Expect(sendEvent(e).Code).To(Equal(http.StatusBadRequest))
		})

		It("rejects bodies over the size limit", func() {
			Expect(send("pull_request", bytes.Repeat([]byte("x"), maxBodyBytes+1)).Code).
				To(Equal(http.StatusRequestEntityTooLarge))
		})
	})

	Describe("opened, reopened and synchronize", func() {
		It("creates the PreviewEnvironment on opened", func() {
			key := keyFor(201)

			Expect(sendEvent(newEvent(actionOpened, 201, testHeadSHA)).Code).To(Equal(http.StatusAccepted))

			env := &previewv1alpha1.PreviewEnvironment{}
			Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
			Expect(env.Spec).To(Equal(previewv1alpha1.PreviewEnvironmentSpec{
				Repository: testRepo,
				Image:      "ghcr.io/seppys/demo-app:" + testHeadSHA,
				Port:       8080,
				PRNumber:   201,
				CommitSHA:  testHeadSHA,
			}))
		})

		It("moves an existing PreviewEnvironment to the new commit on synchronize", func() {
			key := keyFor(202)
			const newSHA = "3333333333333333333333333333333333333333"

			Expect(sendEvent(newEvent(actionOpened, 202, testHeadSHA)).Code).To(Equal(http.StatusAccepted))
			Expect(sendEvent(newEvent(actionSynchronize, 202, newSHA)).Code).To(Equal(http.StatusAccepted))

			env := &previewv1alpha1.PreviewEnvironment{}
			Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
			Expect(env.Spec.CommitSHA).To(Equal(newSHA))
			Expect(env.Spec.Image).To(Equal("ghcr.io/seppys/demo-app:" + newSHA))
		})

		It("creates the PreviewEnvironment when synchronize arrives before opened", func() {
			key := keyFor(203)

			Expect(sendEvent(newEvent(actionSynchronize, 203, testHeadSHA)).Code).To(Equal(http.StatusAccepted))
			Expect(k8sClient.Get(ctx, key, &previewv1alpha1.PreviewEnvironment{})).To(Succeed())
		})

		It("handles a duplicate delivery without error", func() {
			key := keyFor(204)
			e := newEvent(actionReopened, 204, testHeadSHA)

			Expect(sendEvent(e).Code).To(Equal(http.StatusAccepted))
			Expect(sendEvent(e).Code).To(Equal(http.StatusAccepted))
			Expect(k8sClient.Get(ctx, key, &previewv1alpha1.PreviewEnvironment{})).To(Succeed())
		})
	})

	Describe("repository configuration", func() {
		It("does nothing for a repository without .preview-environment.yaml", func() {
			key := keyFor(301)
			fetcher.err = errNoPreviewConfig

			Expect(sendEvent(newEvent(actionOpened, 301, testHeadSHA)).Code).To(Equal(http.StatusNoContent))
			expectNoEnvironment(key)
		})

		It("reports an invalid config with its message", func() {
			key := keyFor(302)
			fetcher.err = fmt.Errorf("%w: port must be between 1 and 65535, got 70000", errInvalidPreviewConfig)

			rec := sendEvent(newEvent(actionOpened, 302, testHeadSHA))
			Expect(rec.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(rec.Body.String()).To(ContainSubstring("got 70000"))
			expectNoEnvironment(key)
		})

		It("fails with a generic message when the config can't be fetched", func() {
			key := keyFor(303)
			fetcher.err = errors.New("dial tcp: connection refused")

			rec := sendEvent(newEvent(actionOpened, 303, testHeadSHA))
			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(rec.Body.String()).NotTo(ContainSubstring("connection refused"), "internal details must not leak")
			expectNoEnvironment(key)
		})
	})

	Describe("closed", func() {
		It("deletes the PreviewEnvironment", func() {
			key := keyFor(401)
			Expect(sendEvent(newEvent(actionOpened, 401, testHeadSHA)).Code).To(Equal(http.StatusAccepted))

			Expect(sendEvent(newEvent(actionClosed, 401, testHeadSHA)).Code).To(Equal(http.StatusAccepted))
			expectNoEnvironment(key)
		})

		It("succeeds when the PR never had a preview", func() {
			key := keyFor(402)
			Expect(sendEvent(newEvent(actionClosed, 402, testHeadSHA)).Code).To(Equal(http.StatusAccepted))
			expectNoEnvironment(key)
		})

		It("doesn't need the repository's config", func() {
			key := keyFor(403)
			Expect(sendEvent(newEvent(actionOpened, 403, testHeadSHA)).Code).To(Equal(http.StatusAccepted))

			fetcher.err = errNoPreviewConfig // the file was removed after the PR was opened
			Expect(sendEvent(newEvent(actionClosed, 403, testHeadSHA)).Code).To(Equal(http.StatusAccepted))
			expectNoEnvironment(key)
		})
	})
})
