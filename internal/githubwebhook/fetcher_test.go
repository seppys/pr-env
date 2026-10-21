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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rawFetcher", func() {
	const (
		owner = "seppys"
		repo  = "my-app"
		sha   = "0123456789abcdef0123456789abcdef01234567"
	)

	// serve starts a fake raw.githubusercontent.com that answers every request with status and
	// body, and records the path it was asked for.
	serve := func(status int, body string) (*rawFetcher, *string) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		DeferCleanup(srv.Close)
		return &rawFetcher{baseURL: srv.URL, http: srv.Client()}, &gotPath
	}

	It("reads the file at the given commit", func() {
		f, gotPath := serve(http.StatusOK, "image: ghcr.io/{owner}/{repo}:{sha}\nport: 8080\n")

		cfg, err := f.fetch(context.Background(), owner, repo, sha)

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).To(Equal(repoConfig{Image: testImageTemplate, Port: 8080}))
		Expect(*gotPath).To(Equal("/seppys/my-app/" + sha + "/.preview-environment.yaml"))
	})

	It("reports a missing file as errNoPreviewConfig", func() {
		f, _ := serve(http.StatusNotFound, "404: Not Found")

		_, err := f.fetch(context.Background(), owner, repo, sha)
		Expect(err).To(MatchError(errNoPreviewConfig))
	})

	It("fails on other statuses without calling the config invalid", func() {
		f, _ := serve(http.StatusInternalServerError, "")

		_, err := f.fetch(context.Background(), owner, repo, sha)
		Expect(err).To(MatchError(ContainSubstring("unexpected status 500")))
		Expect(err).NotTo(MatchError(errInvalidPreviewConfig))
		Expect(err).NotTo(MatchError(errNoPreviewConfig))
	})

	DescribeTable("rejects unusable files as errInvalidPreviewConfig",
		func(body string) {
			f, _ := serve(http.StatusOK, body)

			_, err := f.fetch(context.Background(), owner, repo, sha)
			Expect(err).To(MatchError(errInvalidPreviewConfig))
		},
		Entry("malformed YAML", "image: [unclosed\n"),
		Entry("unknown field, such as a typo", "image: nginx:1.27\nprot: 8080\n"),
		Entry("missing image", "port: 8080\n"),
		Entry("missing port", "image: nginx:1.27\n"),
		Entry("port out of range", "image: nginx:1.27\nport: 70000\n"),
		Entry("larger than the limit", "image: nginx:1.27\nport: 8080\n#"+strings.Repeat("x", maxConfigBytes)),
	)

	It("stops when the context is cancelled", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done() // never answers on its own
		}))
		DeferCleanup(srv.Close)
		f := &rawFetcher{baseURL: srv.URL, http: srv.Client()}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := f.fetch(ctx, owner, repo, sha)
		Expect(err).To(MatchError(context.DeadlineExceeded))
	})
})

var _ = Describe("repoConfig.image", func() {
	DescribeTable("fills in the template",
		func(template, want string) {
			cfg := repoConfig{Image: template, Port: 8080}
			Expect(cfg.image("Seppys", "My-App", "abc123")).To(Equal(want))
		},
		Entry("every placeholder, lowercasing owner and repo",
			testImageTemplate, "ghcr.io/seppys/my-app:abc123"),
		Entry("a fixed image is left as is", "nginx:1.27", "nginx:1.27"),
		Entry("a placeholder used twice", "ghcr.io/{owner}/{repo}:{sha}-{sha}", "ghcr.io/seppys/my-app:abc123-abc123"),
	)
})
