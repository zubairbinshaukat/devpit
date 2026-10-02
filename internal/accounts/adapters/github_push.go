package adapters

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Push protocols.
const (
	ProtocolHTTPS = "https"
	ProtocolSSH   = "ssh"
	ProtocolOther = "other"
)

// GitRemote is a repo's push remote, with any user name or token taken out
// of the URL.
type GitRemote struct {
	Name     string
	URL      string
	Protocol string
	Host     string
}

// IsGitHub reports whether the remote is on github.com.
func (r GitRemote) IsGitHub() bool { return strings.EqualFold(r.Host, githubHost) }

// GitPushVia says what decides the account a push from a folder uses.
type GitPushVia string

const (
	// PushViaDevpit: Devpit's helper answers for github.com here.
	PushViaDevpit GitPushVia = "Devpit's sign-in helper"
	// PushViaHelper: Devpit's helper is not set up; Git's own helpers
	// answer (Git Credential Manager, usually).
	PushViaHelper GitPushVia = "Git's own sign-in helper"
	// PushViaRepoHelper: this repo's own config puts another helper first.
	PushViaRepoHelper GitPushVia = "this repo's own sign-in helper"
	// PushViaSSHKey: the push URL is SSH, so the SSH key decides.
	PushViaSSHKey GitPushVia = "the SSH key"
	// PushViaNotGitHub: the remote is not on github.com.
	PushViaNotGitHub GitPushVia = "not GitHub"
	// PushViaNoRemote: nothing to push to yet.
	PushViaNoRemote GitPushVia = "no remote"
)

// GitHubPush is the Git page's "Pushes as" row for one folder.
type GitHubPush struct {
	Folder string
	Repo   GitRepo
	// Remote is the push remote ("origin" when there is one); HasRemote is
	// false outside a repo or without remotes.
	Remote    GitRemote
	HasRemote bool
	// Resolution is the GitHub account Devpit's rules pick here.
	Resolution accounts.Resolution
	Via        GitPushVia
	// SSHCommand is the effective core.sshCommand (or GIT_SSH_COMMAND) and
	// SSHFrom where it came from; SSHKey is the key Devpit set for this
	// folder or a folder around it, if any.
	SSHCommand string
	SSHFrom    GitFrom
	SSHKey     string
	// HelperInstalled: Devpit's github.com helper is set up.
	HelperInstalled bool
	Notes           []string
}

// PushesAs works out which account a push from folder uses and why:
// HTTPS remotes go through the credential helper chain (Devpit's helper
// when it is set up), SSH remotes through the SSH key.
func (a *GitHubAdapter) PushesAs(ctx context.Context, s *accounts.Store, folder string) (GitHubPush, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	out := GitHubPush{Folder: folder}
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
		return out, fmt.Errorf("the folder %s was not found", folder)
	}
	res, err := accounts.Resolve(s, accounts.ToolGitHub, folder, accounts.ResolveOptions{RealPath: accounts.RealPath})
	if err != nil {
		return out, err
	}
	out.Resolution = res
	g := gitSync{d: a.deps, dir: GitDir(a.deps), global: gitGlobalPath(a.deps)}
	x, err := readGitExtras(g.dir)
	if err != nil {
		return out, err
	}
	out.HelperInstalled = x.Helper != nil
	best := -1
	for f, k := range x.SSHKeys {
		if accounts.FolderContains(f, folder) {
			if d := winDepth(f); d > best {
				best, out.SSHKey = d, k
			}
		}
	}

	if out.Repo, err = gitRepoInfo(ctx, a.deps, folder); err != nil {
		return out, err
	}
	note := func(f string, args ...any) { out.Notes = append(out.Notes, fmt.Sprintf(f, args...)) }
	if !out.Repo.IsRepo {
		out.Via = PushViaNoRemote
		note("This folder is not a Git repo yet. Pushes from repos inside it will use %s over HTTPS.", accountName(res.Account))
		return out, nil
	}
	out.Remote, out.HasRemote, err = pushRemote(ctx, a.deps, folder)
	if err != nil {
		return out, err
	}
	// Devpit's SSH keys are always folder rules.
	if v, verr := g.effective(ctx, folder, "core.sshCommand", accounts.Resolution{Reason: accounts.ReasonFolderRule}); verr == nil && v.Value != "" {
		out.SSHCommand, out.SSHFrom = v.Value, v.From
	}
	if v := a.deps.getenv("GIT_SSH_COMMAND"); v != "" {
		out.SSHCommand, out.SSHFrom = accounts.Scrub(v), GitFromCommand
	}
	switch {
	case !out.HasRemote:
		out.Via = PushViaNoRemote
		note("This repo has no remote yet.")
	case !out.Remote.IsGitHub():
		out.Via = PushViaNotGitHub
		note("This repo pushes to %s, not GitHub, so Devpit's GitHub rules do not apply.", firstOf(out.Remote.Host, out.Remote.URL))
	case out.Remote.Protocol == ProtocolSSH:
		out.Via = PushViaSSHKey
		key := "the keys ssh offers by default (~/.ssh/id_ed25519, id_rsa…)"
		if out.SSHCommand != "" {
			key = out.SSHCommand
		}
		note("This repo pushes over SSH, so GitHub sees the account that owns the SSH key (%s), not %s.", key, accountName(res.Account))
	default:
		out.Via, err = a.httpsVia(ctx, folder, x)
		if err != nil {
			return out, err
		}
		switch out.Via {
		case PushViaHelper:
			if !res.Account.IsDefault() {
				note("Devpit's sign-in helper is not set up, so pushes use your usual GitHub sign-in, not %s.", accountName(res.Account))
			}
		case PushViaRepoHelper:
			note("This repo's own config puts another sign-in helper first, so it decides the account, not Devpit.")
		}
	}
	return out, nil
}

func accountName(a accounts.Account) string {
	if a.IsDefault() {
		return "your default GitHub sign-in"
	}
	return a.Display()
}

// httpsVia works out which helper Git asks first for github.com in folder:
// every scope in order, resets applied.
func (a *GitHubAdapter) httpsVia(ctx context.Context, folder string, x gitExtras) (GitPushVia, error) {
	if x.Helper == nil {
		return PushViaHelper, nil
	}
	res, err := a.deps.run(ctx, Cmd{Name: "git", Dir: folder, Args: []string{
		"config", "--show-origin", "--show-scope", "-z", "--get-regexp", `^credential\..*helper$`,
	}})
	if err != nil {
		return "", accounts.ScrubError(err)
	}
	recs := zRecords(res.Stdout)
	var chain []string
	repo := false
	for i := 0; i+2 < len(recs); i += 3 {
		key, val, _ := strings.Cut(recs[i+2], "\n")
		if !helperKeyMatchesGitHub(key) {
			continue
		}
		if recs[i] == "local" || recs[i] == "worktree" {
			repo = true
		}
		if val == "" {
			chain = nil
			continue
		}
		chain = append(chain, val)
	}
	if len(chain) > 0 && chain[0] == x.Helper.Command {
		return PushViaDevpit, nil
	}
	if repo {
		return PushViaRepoHelper, nil
	}
	return PushViaHelper, nil
}

// pushRemote finds the remote a plain `git push` uses ("origin", else the
// first) and its push URL.
func pushRemote(ctx context.Context, d Deps, folder string) (GitRemote, bool, error) {
	res, err := d.run(ctx, Cmd{Name: "git", Dir: folder, Args: []string{"remote"}})
	if err != nil {
		return GitRemote{}, false, accounts.ScrubError(err)
	}
	names := Lines(res.Stdout)
	if len(names) == 0 {
		return GitRemote{}, false, nil
	}
	name := names[0]
	for _, n := range names {
		if n == "origin" {
			name = n
		}
	}
	res, err = d.run(ctx, Cmd{Name: "git", Dir: folder, Args: []string{"remote", "get-url", "--push", name}})
	if err != nil {
		return GitRemote{}, false, accounts.ScrubError(err)
	}
	if res.ExitCode != 0 {
		return GitRemote{}, false, nil
	}
	r := ParseRemoteURL(strings.TrimSpace(string(res.Stdout)))
	r.Name = name
	return r, true, nil
}

// ParseRemoteURL classifies a remote URL: https://…, ssh://…, git@host:…
// (scp-like SSH). A user name or token in an HTTPS URL is removed.
func ParseRemoteURL(raw string) GitRemote {
	r := GitRemote{URL: raw, Protocol: ProtocolOther}
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://"):
		r.Protocol = ProtocolHTTPS
		if u, err := url.Parse(raw); err == nil {
			u.User = nil
			r.URL, r.Host = u.String(), u.Hostname()
		}
	case strings.HasPrefix(low, "ssh://") || strings.HasPrefix(low, "git+ssh://") || strings.HasPrefix(low, "ssh+git://"):
		r.Protocol = ProtocolSSH
		if u, err := url.Parse(raw); err == nil {
			r.Host = u.Hostname()
		}
	case !strings.Contains(raw, "://"):
		// scp-like: [user@]host:path, but not C:/path or a plain path.
		hostPart, _, ok := strings.Cut(raw, ":")
		if ok && len(hostPart) > 1 && !strings.ContainsAny(hostPart, `/\`) {
			r.Protocol = ProtocolSSH
			if _, h, found := strings.Cut(hostPart, "@"); found {
				hostPart = h
			}
			r.Host = hostPart
		}
	}
	r.URL = accounts.Scrub(r.URL)
	return r
}
