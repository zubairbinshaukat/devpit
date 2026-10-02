package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// EnvGitHubAccount names a Devpit GitHub account for one process tree. The
// gh shim sets it next to GH_TOKEN, so a git that gh starts (gh repo clone)
// pushes as the same account, and `devpit github run <name> -- git push`
// uses it for that one push. Devpit's push helper honours it before the
// folder rules.
const EnvGitHubAccount = "DEVPIT_GITHUB_ACCOUNT"

// ErrNeedsLiveLaunch is wrapped by the static Launch for an account whose
// launch needs a tool run first (GitHub: the token comes from `gh auth
// token` when gh starts). Call LaunchLive.
var ErrNeedsLiveLaunch = errors.New("this account is applied by asking the tool when it starts; the launcher must call LaunchLive")

// LaunchLive is what the shim and `devpit <tool> run` apply for acct. For
// every tool but GitHub it is LaunchFor and runs nothing. For a named
// GitHub account it runs `gh auth token --hostname github.com --user
// <login>` through r (the real gh, outside the shim folder) and returns
// GH_TOKEN for the child.
//
// That token is the one secret that ever passes through Devpit: it lives in
// the returned Launch.Env only, goes from there into the child's
// environment, and is never logged, put in an event or an error, or
// written anywhere. Callers must not print Launch.Env.
func LaunchLive(ctx context.Context, r Runner, acct accounts.Account) (Launch, error) {
	if acct.Tool != accounts.ToolGitHub || acct.IsDefault() {
		return LaunchFor(acct)
	}
	if !validLogin(acct.Label) {
		return Launch{}, fmt.Errorf("the GitHub account %s has no GitHub login recorded; sign in again", acct.Name)
	}
	tok, err := ghToken(ctx, r, acct.Label)
	if err != nil {
		return Launch{}, err
	}
	return Launch{
		Env:   []string{"GH_TOKEN=" + tok, EnvGitHubAccount + "=" + acct.Name},
		Unset: []string{"GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"},
	}, nil
}

// GitHubToken returns the token gh holds for login on github.com, for the
// push helper (package gitcred). The same rules as LaunchLive apply: the
// caller writes it to exactly one place (the child's environment, or
// Git's credential protocol on stdout) and nowhere else.
func GitHubToken(ctx context.Context, r Runner, login string) (string, error) {
	if !validLogin(login) {
		return "", fmt.Errorf("%q is not a GitHub login", login)
	}
	return ghToken(ctx, r, login)
}

// ghToken asks gh for login's token: `gh auth token --hostname github.com
// --user <login>`, with every token variable removed so gh answers from
// its own storage. Its output is checked to be one token and nothing else;
// an error never carries it.
func ghToken(ctx context.Context, r Runner, login string) (string, error) {
	if r == nil {
		return "", errors.New("no command runner")
	}
	res, err := r.Run(ctx, Cmd{
		Name: "gh", Args: []string{"auth", "token", "--hostname", githubHost, "--user", login},
		Unset: ghClean(), Timeout: 15 * time.Second,
	})
	if err != nil {
		return "", accounts.ScrubError(err)
	}
	tok := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || tok == "" {
		return "", fmt.Errorf("gh has no working sign-in for %s on github.com (%s). Sign in again with: gh auth login",
			login, firstOf(FirstLine(res.Stderr), "no token"))
	}
	if !validToken(tok) {
		return "", fmt.Errorf("gh answered `gh auth token` for %s with something that is not a token", login)
	}
	return tok, nil
}

// validToken: one line of printable ASCII without spaces, of a sane length.
func validToken(t string) bool {
	if len(t) < 8 || len(t) > 1024 {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] <= ' ' || t[i] > '~' {
			return false
		}
	}
	return true
}
