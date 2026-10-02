package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// EmailSuggestion is an address the Git page offers for a commit identity.
type EmailSuggestion struct {
	Email string
	// Why is a short reason: "GitHub's private address for zubair".
	Why      string
	Primary  bool
	Verified bool
}

// NoReplyEmail is GitHub's private commit address for an account:
// ID+login@users.noreply.github.com.
func NoReplyEmail(id int64, login string) string {
	return fmt.Sprintf("%d+%s@users.noreply.github.com", id, login)
}

// SuggestEmails offers commit addresses for a GitHub account: its
// noreply address always (from `gh api user`), then its verified addresses,
// but only when its token already has the user:email (or user) scope.
// Devpit never asks gh for more scopes, so nothing is refreshed or
// re-authorised behind the person's back.
func (a *GitHubAdapter) SuggestEmails(ctx context.Context, acct accounts.Account) ([]EmailSuggestion, error) {
	u, id, err := a.apiUser(ctx, acct)
	if err != nil {
		return nil, err
	}
	if !id.SignedIn() {
		return nil, fmt.Errorf("the GitHub account %s is %s", acct.Name, id.State())
	}
	out := []EmailSuggestion{{
		Email: NoReplyEmail(u.ID, u.Login), Verified: true,
		Why: "GitHub's private address for " + u.Login + ": commits link to the account without showing your email",
	}}

	logins, err := ghLogins(ctx, a.deps)
	if err != nil {
		return out, nil //nolint:nilerr // the noreply address is still a good answer
	}
	scoped := false
	for _, l := range logins {
		if strings.EqualFold(l.Login, u.Login) {
			scoped = l.hasScope("user:email") || l.hasScope("user")
		}
	}
	if !scoped {
		return out, nil
	}
	cmd := Cmd{Name: "gh", Args: []string{"api", "--hostname", githubHost, "user/emails"}, Unset: ghClean(), Timeout: 30 * time.Second}
	if !acct.IsDefault() {
		tok, terr := ghToken(ctx, a.deps.Runner, acct.Label)
		if terr != nil {
			return out, nil //nolint:nilerr // as above
		}
		cmd.Env = []string{"GH_TOKEN=" + tok}
	}
	res, err := a.deps.run(ctx, cmd)
	if err != nil || res.ExitCode != 0 {
		return out, nil //nolint:nilerr // as above
	}
	emails, err := decodeEmails(res.Stdout)
	if err != nil {
		return out, nil //nolint:nilerr // as above
	}
	for _, e := range emails {
		if !e.Verified || e.Email == "" || strings.EqualFold(e.Email, out[0].Email) || accounts.LooksSecret(e.Email) {
			continue
		}
		why := "verified on GitHub"
		if e.Primary {
			why = "your primary address on GitHub"
		}
		out = append(out, EmailSuggestion{Email: accounts.Scrub(e.Email), Why: why, Primary: e.Primary, Verified: true})
	}
	return out, nil
}
