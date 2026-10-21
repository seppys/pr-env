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
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testOwner    = "seppys"
	testRepoName = "demo-app"
	testRepo     = testOwner + "/" + testRepoName
	testHeadSHA  = "1111111111111111111111111111111111111111"
	testBaseSHA  = "2222222222222222222222222222222222222222"
	// testImageTemplate is a typical image template from .preview-environment.yaml.
	testImageTemplate = "ghcr.io/{owner}/{repo}:{sha}"
)

// fakeFetcher returns a fixed config or error, and records which commit it was asked for.
type fakeFetcher struct {
	cfg    repoConfig
	err    error
	gotSHA string
}

func (f *fakeFetcher) fetch(_ context.Context, _, _, sha string) (repoConfig, error) {
	f.gotSHA = sha
	return f.cfg, f.err
}

// newEvent returns a valid pull_request event for testRepo.
func newEvent(a action, number int32, headSHA string) pullRequestEvent {
	repo := repository{FullName: testRepo, Name: testRepoName, Owner: githubUser{Login: testOwner}}
	return pullRequestEvent{
		Action: a,
		Number: number,
		PullRequest: pullRequest{
			Head: branch{SHA: headSHA, Repo: &repo},
			Base: branch{SHA: testBaseSHA, Repo: &repo},
		},
		Repository: repo,
	}
}

var _ = Describe("environmentName", func() {
	DescribeTable("builds a valid, deterministic DNS label",
		func(repository string, prNumber int32, want string) {
			got := environmentName(repository, prNumber)
			Expect(got).To(Equal(want))
			Expect(len(got)).To(BeNumerically("<=", maxNameLength))
		},
		Entry("a plain repository", "seppys/my-app", int32(12), "my-app-pr-12"),
		Entry("uses only the repository part of owner/repo", "Someone/app", int32(1), "app-pr-1"),
		Entry("lowercases and replaces invalid characters", "seppys/My_Web.App", int32(3), "my-web-app-pr-3"),
		Entry("works without an owner", "lonely", int32(7), "lonely-pr-7"),
		Entry("truncates long names but keeps the PR number",
			"seppys/"+strings.Repeat("a", 70), int32(42), strings.Repeat("a", 57)+"-pr-42"),
		Entry("trims a dash left at the end by truncation",
			"seppys/"+strings.Repeat("a", 56)+"-b", int32(42), strings.Repeat("a", 56)+"-pr-42"),
		Entry("falls back when nothing valid is left", "seppys/___", int32(5), "repo-pr-5"),
	)
})

var _ = Describe("previewFor", func() {
	It("reads the config at the base commit and tags the image with the head commit", func() {
		fetcher := &fakeFetcher{cfg: repoConfig{Image: testImageTemplate, Port: 3000}}
		s := &service{configs: fetcher}

		p, err := s.previewFor(context.Background(), newEvent(actionOpened, 9, testHeadSHA))

		Expect(err).NotTo(HaveOccurred())
		Expect(fetcher.gotSHA).To(Equal(testBaseSHA), "the config must come from the trusted base branch")
		Expect(p).To(Equal(preview{
			Repository: testRepo,
			PRNumber:   9,
			CommitSHA:  testHeadSHA,
			Image:      "ghcr.io/seppys/demo-app:" + testHeadSHA,
			Port:       3000,
		}))
	})

	It("returns the fetcher's error unchanged", func() {
		s := &service{configs: &fakeFetcher{err: errNoPreviewConfig}}

		_, err := s.previewFor(context.Background(), newEvent(actionOpened, 9, testHeadSHA))
		Expect(errors.Is(err, errNoPreviewConfig)).To(BeTrue())
	})
})

var _ = Describe("pullRequestEvent.validate", func() {
	DescribeTable("rejects payloads missing the fields pr-env needs",
		func(mutate func(*pullRequestEvent)) {
			e := newEvent(actionOpened, 1, testHeadSHA)
			mutate(&e)
			Expect(e.validate()).NotTo(Succeed())
		},
		Entry("no number", func(e *pullRequestEvent) { e.Number = 0 }),
		Entry("no repository", func(e *pullRequestEvent) { e.Repository = repository{} }),
		Entry("no owner", func(e *pullRequestEvent) { e.Repository.Owner.Login = "" }),
		Entry("no head sha", func(e *pullRequestEvent) { e.PullRequest.Head.SHA = "" }),
		Entry("no base sha", func(e *pullRequestEvent) { e.PullRequest.Base.SHA = "" }),
	)

	It("accepts a complete payload", func() {
		Expect(newEvent(actionOpened, 1, testHeadSHA).validate()).To(Succeed())
	})
})
