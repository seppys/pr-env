package githubwebhook

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	previewv1alpha1 "github.com/seppys/pr-env/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// maxNameLength is the longest valid DNS label. The name is also used as the preview's namespace.
const maxNameLength = 63

var invalidNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

type service struct {
	client    client.Client
	namespace string
	configs   configFetcher
}

// previewFor builds the preview a pull_request event asks for from the repository's
// .preview-environment.yaml.
func (s *service) previewFor(ctx context.Context, event pullRequestEvent) (preview, error) {
	repo := event.Repository
	// The config is read at the base commit (trusted: it only changes through merges), while the
	// image is tagged with the head commit (the PR's code).
	cfg, err := s.configs.fetch(ctx, repo.Owner.Login, repo.Name, event.PullRequest.Base.SHA)
	if err != nil {
		return preview{}, err
	}

	head := event.PullRequest.Head.SHA
	return preview{
		Repository: repo.FullName,
		PRNumber:   event.Number,
		CommitSHA:  head,
		Image:      cfg.image(repo.Owner.Login, repo.Name, head),
		Port:       cfg.Port,
	}, nil
}

func (s *service) upsert(ctx context.Context, p preview) error {
	env := &previewv1alpha1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{
		Name:      environmentName(p.Repository, p.PRNumber),
		Namespace: s.namespace,
	}}

	op, err := controllerutil.CreateOrUpdate(ctx, s.client, env, func() error {
		env.Spec = previewv1alpha1.PreviewEnvironmentSpec{
			Repository: p.Repository,
			Image:      p.Image,
			Port:       p.Port,
			PRNumber:   p.PRNumber,
			CommitSHA:  p.CommitSHA,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("applying PreviewEnvironment %s/%s: %w", env.Namespace, env.Name, err)
	}
	logf.FromContext(ctx).Info("Applied PreviewEnvironment", "name", env.Name, "operation", op)
	return nil
}

// delete removes a PR's PreviewEnvironment; its finalizer cleans up the rest. It only needs the
// name, so it never fetches the config: a repo that has since removed the file still gets cleaned up.
func (s *service) delete(ctx context.Context, repository string, prNumber int32) error {
	env := &previewv1alpha1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{
		Name:      environmentName(repository, prNumber),
		Namespace: s.namespace,
	}}

	if err := s.client.Delete(ctx, env); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting PreviewEnvironment %s/%s: %w", env.Namespace, env.Name, err)
	}
	return nil
}

// environmentName is the PreviewEnvironment name for a PR: <repo>-pr-<number>, as a valid DNS label.
func environmentName(repository string, prNumber int32) string {
	repo := repository[strings.LastIndex(repository, "/")+1:]
	suffix := fmt.Sprintf("-pr-%d", prNumber)

	repo = invalidNameChars.ReplaceAllString(strings.ToLower(repo), "-")
	if limit := maxNameLength - len(suffix); len(repo) > limit {
		repo = repo[:limit]
	}
	repo = strings.Trim(repo, "-")
	if repo == "" {
		repo = "repo"
	}
	return repo + suffix
}
