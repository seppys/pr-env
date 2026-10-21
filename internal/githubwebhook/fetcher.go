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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	// previewConfigPath is where a repository describes how its previews are deployed.
	previewConfigPath = ".preview-environment.yaml"
	maxConfigBytes    = 64 << 10
	rawGitHubURL      = "https://raw.githubusercontent.com"
	// fetchTimeout bounds the request; GitHub gives the whole webhook delivery 10 seconds.
	fetchTimeout = 5 * time.Second
)

var (
	// errNoPreviewConfig means the repository has no .preview-environment.yaml, so it hasn't
	// opted into previews.
	errNoPreviewConfig = errors.New("repository has no " + previewConfigPath)
	// errInvalidPreviewConfig means the file exists but can't be used.
	errInvalidPreviewConfig = errors.New("invalid " + previewConfigPath)
)

// repoConfig is the content of a repository's .preview-environment.yaml.
type repoConfig struct {
	// Image is the image template. {owner}, {repo} and {sha} are replaced when a preview is built.
	Image string `json:"image"`
	// Port is the port the application listens on.
	Port int32 `json:"port"`
}

func (c repoConfig) validate() error {
	if strings.TrimSpace(c.Image) == "" {
		return fmt.Errorf("%w: image is required", errInvalidPreviewConfig)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535, got %d", errInvalidPreviewConfig, c.Port)
	}
	return nil
}

// image fills in the image template for a commit. Owner and repo are lowercased because image
// references must be lowercase, while GitHub names may not be.
func (c repoConfig) image(owner, repo, sha string) string {
	return strings.NewReplacer(
		"{owner}", strings.ToLower(owner),
		"{repo}", strings.ToLower(repo),
		"{sha}", sha,
	).Replace(c.Image)
}

// configFetcher returns a repository's preview configuration at a given commit.
type configFetcher interface {
	fetch(ctx context.Context, owner, repo, sha string) (repoConfig, error)
}

// rawFetcher reads .preview-environment.yaml from raw.githubusercontent.com. It only works for
// public repositories: private ones need an authenticated fetcher.
type rawFetcher struct {
	baseURL string
	http    *http.Client
}

var _ configFetcher = &rawFetcher{}

func newRawFetcher() *rawFetcher {
	return &rawFetcher{baseURL: rawGitHubURL, http: &http.Client{Timeout: fetchTimeout}}
}

// fetch reads the file at sha. Callers should pass the PR's base commit, not its head: the base
// branch only changes through merges, so a pull request can't rewrite its own configuration.
// Fetching by commit SHA also keeps the CDN from serving a stale copy, which a branch name can.
func (f *rawFetcher) fetch(ctx context.Context, owner, repo, sha string) (repoConfig, error) {
	fileURL, err := url.JoinPath(f.baseURL,
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha), previewConfigPath)
	if err != nil {
		return repoConfig{}, fmt.Errorf("building %s URL: %w", previewConfigPath, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return repoConfig{}, fmt.Errorf("building %s request: %w", previewConfigPath, err)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return repoConfig{}, fmt.Errorf("fetching %s: %w", previewConfigPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return repoConfig{}, errNoPreviewConfig
	default:
		return repoConfig{}, fmt.Errorf("fetching %s: unexpected status %s", previewConfigPath, resp.Status)
	}

	// Read one byte past the limit to tell "exactly at the limit" from "too large".
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigBytes+1))
	if err != nil {
		return repoConfig{}, fmt.Errorf("reading %s: %w", previewConfigPath, err)
	}
	if len(data) > maxConfigBytes {
		return repoConfig{}, fmt.Errorf("%w: larger than %d bytes", errInvalidPreviewConfig, maxConfigBytes)
	}

	var cfg repoConfig
	// Strict, so a typo like "prot: 8080" is reported instead of silently ignored.
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return repoConfig{}, fmt.Errorf("%w: %w", errInvalidPreviewConfig, err)
	}
	if err := cfg.validate(); err != nil {
		return repoConfig{}, err
	}
	return cfg, nil
}
