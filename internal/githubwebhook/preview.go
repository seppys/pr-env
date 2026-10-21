package githubwebhook

type preview struct {
	Repository string
	PRNumber   int32
	CommitSHA  string
	Image      string
	Port       int32
}
