package githubwebhook

import "errors"

type action string

const (
	actionOpened      action = "opened"
	actionReopened    action = "reopened"
	actionClosed      action = "closed"
	actionSynchronize action = "synchronize"
)

// pullRequestEvent is the subset of GitHub's pull_request webhook payload pr-env uses.
type pullRequestEvent struct {
	Action      action      `json:"action"`
	Number      int32       `json:"number"`
	PullRequest pullRequest `json:"pull_request"`
	// Repository is the repository the webhook belongs to: the PR's base, never a fork.
	Repository repository `json:"repository"`
}

type pullRequest struct {
	Head branch `json:"head"`
	Base branch `json:"base"`
}

type branch struct {
	SHA string `json:"sha"`
	// Repo is null when the fork the pull request came from has been deleted.
	Repo *repository `json:"repo"`
}

type repository struct {
	FullName string     `json:"full_name"`
	Name     string     `json:"name"`
	Owner    githubUser `json:"owner"`
}

type githubUser struct {
	Login string `json:"login"`
}

// validate checks the fields pr-env relies on. Missing JSON keys decode to zero values without an
// error, so a well-formed payload can still be unusable.
func (e pullRequestEvent) validate() error {
	switch {
	case e.Number <= 0:
		return errors.New("number is missing")
	case e.Repository.FullName == "" || e.Repository.Name == "" || e.Repository.Owner.Login == "":
		return errors.New("repository is missing")
	case e.PullRequest.Head.SHA == "" || e.PullRequest.Base.SHA == "":
		return errors.New("pull_request head or base sha is missing")
	}
	return nil
}
