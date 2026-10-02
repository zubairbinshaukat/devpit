package adapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// gitSync works out, and then makes, every file change the Git side of an
// accounts change needs. The Git and GitHub adapters share it: Devpit's Git
// files are always regenerated whole from the store as it will be after the
// change, so the two can never disagree.
type gitSync struct {
	d   Deps
	dir string
	// global is the person's global config file.
	global string
	// worktree: also write `worktree/i:` blocks (the probe proved they work).
	worktree bool
	// exe is devpit.exe for the credential helper line. "" keeps the one
	// already recorded, or uses this program.
	exe string
}

func newGitSync(ctx context.Context, d Deps, p *gitProber, exe string) (gitSync, error) {
	c, err := p.get(ctx, d)
	if err = gitCheck(c, err); err != nil {
		return gitSync{}, err
	}
	return gitSync{d: d, dir: GitDir(d), global: gitGlobalPath(d), worktree: c.worktree, exe: exe}, nil
}

// gitOp is one file change.
type gitOp struct {
	path string
	// data is the new content; nil removes the file.
	data   []byte
	before []byte
	exists bool
	global bool
	// lines, when set, is what the preview shows instead of the whole file
	// (the global config shows only Devpit's block).
	lines []string
	// action overrides the preview's verb ("adds", "removes").
	action string
	what   string
}

func (o gitOp) remove() bool { return o.data == nil }

// gitPlan is the file changes, in the order they are made, plus warnings.
type gitPlan struct {
	ops      []gitOp
	warnings []string
	extras   gitExtras
}

// edits is the preview's view of the plan: path, action and exact lines.
func (p gitPlan) edits(home string) []accounts.FileEdit {
	out := make([]accounts.FileEdit, 0, len(p.ops))
	for _, o := range p.ops {
		e := accounts.FileEdit{Path: displayPath(home, o.path), Action: o.action}
		switch {
		case e.Action != "":
		case o.remove():
			e.Action = "removes"
		case o.exists:
			e.Action = "changes"
		default:
			e.Action = "adds"
		}
		switch {
		case o.lines != nil:
			e.Lines = o.lines
		case o.remove():
			e.Lines = fileLines(o.before)
		default:
			e.Lines = fileLines(o.data)
		}
		out = append(out, e)
	}
	return out
}

// githubHelperNeeded reports whether Devpit must answer for github.com
// pushes: some folder rule names a GitHub account (a "default" one inside
// another rule included), or "everywhere" names one.
func githubHelperNeeded(s *accounts.Store) bool {
	if v, ok := s.Everywhere[accounts.ToolGitHub]; ok && !accounts.IsDefault(v) {
		return true
	}
	for _, r := range s.Rules {
		if _, ok := r.Accounts[accounts.ToolGitHub]; ok {
			return true
		}
	}
	return false
}

// helperCommand is the helper line for exe, quoted the way Git for
// Windows writes its own Git Credential Manager line.
func helperCommand(exe string) string {
	return `!"` + slashPath(exe) + `" git-credential`
}

// helperExe is the program a recorded helper command runs, or "".
func helperExe(cmd string) string {
	s := strings.TrimPrefix(cmd, "!")
	if strings.HasPrefix(s, `"`) {
		if i := strings.IndexByte(s[1:], '"'); i >= 0 {
			return s[1 : i+1]
		}
	}
	return ""
}

// identityFileName is the file for an identity: "<name>.gitconfig", except
// for names that would clash with Devpit's other files.
func identityFileName(name string) string {
	n := strings.ToLower(name)
	if n == "rules" || n == "default" || strings.HasPrefix(n, "ssh-") || strings.HasPrefix(n, "identity-") {
		n = "identity-" + n
	}
	return n + ".gitconfig"
}

func sshFileName(folder string) string {
	norm, err := accounts.NormalizeFolder(folder, "")
	if err != nil {
		norm = folder
	}
	sum := sha256.Sum256([]byte(strings.ToUpper(norm)))
	return "ssh-" + hex.EncodeToString(sum[:4]) + ".gitconfig"
}

// identityFile renders one identity's file.
func identityFile(a accounts.Account, sg GitSigning) ([]byte, error) {
	lines := []string{
		gitHeader + " The Git identity " + a.Name + ": Devpit rewrites this file from accounts.toml, so change the identity in Devpit.",
		"[user]",
	}
	add := func(key, val string) error {
		q, err := gitQuote(val)
		if err != nil {
			return fmt.Errorf("the %s of the Git identity %s: %w", key, a.Name, err)
		}
		lines = append(lines, gitIndent+key+" = "+q)
		return nil
	}
	if a.Label != "" {
		if err := add("name", a.Label); err != nil {
			return nil, err
		}
	}
	if a.Email == "" {
		return nil, fmt.Errorf("the Git identity %s has no email", a.Name)
	}
	if err := add("email", a.Email); err != nil {
		return nil, err
	}
	if err := sg.validate(); err != nil {
		return nil, fmt.Errorf("the Git identity %s: %w", a.Name, err)
	}
	if sg.Key != "" {
		if err := add("signingkey", sg.Key); err != nil {
			return nil, err
		}
	}
	if sg.Format != "" {
		lines = append(lines, "[gpg]", gitIndent+"format = "+sg.Format)
	}
	if sg.Sign {
		lines = append(lines, "[commit]", gitIndent+"gpgsign = true")
	}
	return sealGitFile(lines), nil
}

// defaultFile renders the person's own identity, for a folder whose rule
// says "default" inside a folder ruled otherwise.
func defaultFile(name, email string) ([]byte, error) {
	lines := []string{gitHeader + " Your own Git identity, as your global config says, for folders whose rule is \"default\"."}
	if email == "" && name == "" {
		lines = append(lines, "# Your global config sets no identity, so there is nothing to go back to.")
		return sealGitFile(lines), nil
	}
	lines = append(lines, "[user]")
	for _, kv := range [][2]string{{"name", name}, {"email", email}} {
		if kv[1] == "" {
			continue
		}
		q, err := gitQuote(kv[1])
		if err != nil {
			return nil, fmt.Errorf("your own Git %s: %w", kv[0], err)
		}
		lines = append(lines, gitIndent+kv[0]+" = "+q)
	}
	return sealGitFile(lines), nil
}

func sshFile(folder, key string) ([]byte, error) {
	q, err := gitQuote(sshCommand(key))
	if err != nil {
		return nil, fmt.Errorf("the SSH key for %s: %w", folder, err)
	}
	return sealGitFile([]string{
		gitHeader + " The SSH key Git uses to push from " + folder + ".",
		"[core]",
		gitIndent + "sshCommand = " + q,
	}), nil
}

// winDepth counts the folders below the drive or share of a normalized
// Windows path: 0 for C:\ and \\server\share.
func winDepth(norm string) int {
	return max(0, accounts.FolderDepth(norm))
}

type gitBlock struct {
	folder string
	depth  int
	key    string
	paths  []string
}

// compute works out the files for store s and extras x as they will be
// after a change, compares them with what is on disk, and returns the
// changes. It writes nothing.
func (g gitSync) compute(ctx context.Context, s *accounts.Store, x gitExtras) (gitPlan, error) {
	plan := gitPlan{}
	x = x.clone()

	// The credential helper for github.com.
	if githubHelperNeeded(s) {
		exe := g.exe
		// No program given: keep the recorded one while it still exists
		// (Devpit reinstalled elsewhere would leave pushes with no helper).
		if exe == "" && (x.Helper == nil || !fileExists(filepath.FromSlash(helperExe(x.Helper.Command)))) {
			self, err := os.Executable()
			if err != nil {
				return plan, fmt.Errorf("cannot find Devpit's own program for Git's sign-in helper: %w", err)
			}
			exe = self
		}
		if x.Helper == nil {
			prev, err := g.previousHelpers(ctx)
			if err != nil {
				return plan, err
			}
			x.Helper = &GitHelper{Previous: prev}
		}
		if exe != "" {
			x.Helper.Command = helperCommand(exe)
		}
	} else {
		x.Helper = nil
	}
	plan.extras = x

	want := map[string][]byte{}
	blocks := map[string]*gitBlock{}
	block := func(folder string) (*gitBlock, error) {
		norm, err := accounts.NormalizeFolder(folder, "")
		if err != nil {
			return nil, err
		}
		k := strings.ToUpper(norm)
		if b, ok := blocks[k]; ok {
			return b, nil
		}
		b := &gitBlock{folder: norm, depth: winDepth(norm), key: k}
		blocks[k] = b
		return b, nil
	}
	identity := func(a accounts.Account) (string, error) {
		name := identityFileName(a.Name)
		if _, done := want[name]; !done {
			data, err := identityFile(a, x.signingFor(a.Name))
			if err != nil {
				return "", err
			}
			want[name] = data
		}
		return name, nil
	}

	everywhere := ""
	if v, ok := s.Everywhere[accounts.ToolGit]; ok && !accounts.IsDefault(v) {
		if a, ok := s.Account(accounts.ToolGit, v); ok {
			f, err := identity(a)
			if err != nil {
				return plan, err
			}
			everywhere = f
		} else {
			plan.warnings = append(plan.warnings, fmt.Sprintf("Git is set to use %q everywhere, but there is no such identity, so your own global identity stays in use.", v))
		}
	}
	needDefault := false
	for _, r := range s.Rules {
		name, ok := r.Accounts[accounts.ToolGit]
		if !ok {
			continue
		}
		a, ok := s.Account(accounts.ToolGit, name)
		if !ok {
			plan.warnings = append(plan.warnings, fmt.Sprintf("The rule on %s names the Git identity %q, which no longer exists, so Git skips it.", r.Folder, name))
			continue
		}
		f := gitDefaultFile
		if a.IsDefault() {
			needDefault = true
		} else {
			var err error
			if f, err = identity(a); err != nil {
				return plan, err
			}
		}
		b, err := block(r.Folder)
		if err != nil {
			return plan, err
		}
		b.paths = append(b.paths, f)
	}
	if needDefault {
		name, email, err := g.ownIdentity(ctx)
		if err != nil {
			return plan, err
		}
		if email == "" {
			plan.warnings = append(plan.warnings, "Your global Git config sets no user.email, so a folder whose rule is \"default\" keeps the identity of the folder around it.")
		}
		data, err := defaultFile(name, email)
		if err != nil {
			return plan, err
		}
		want[gitDefaultFile] = data
	}
	sshFolders := make([]string, 0, len(x.SSHKeys))
	for f := range x.SSHKeys {
		sshFolders = append(sshFolders, f)
	}
	slices.Sort(sshFolders)
	for _, f := range sshFolders {
		name := sshFileName(f)
		data, err := sshFile(f, x.SSHKeys[f])
		if err != nil {
			return plan, err
		}
		want[name] = data
		b, err := block(f)
		if err != nil {
			return plan, err
		}
		b.paths = append(b.paths, name)
	}

	// rules.gitconfig.
	head := []string{
		gitHeader + " Devpit rewrites this file from accounts.toml on every change: change the rules in Devpit, not here.",
		"# Later blocks win, so folders are listed from the outside in.",
	}
	lines := slices.Clone(head)
	if everywhere != "" {
		q, _ := gitQuote(everywhere)
		lines = append(lines, "[include]", gitIndent+"path = "+q)
	}
	if x.Helper != nil {
		q, err := gitQuote(x.Helper.Command)
		if err != nil {
			return plan, fmt.Errorf("Git's sign-in helper: %w", err) //nolint:revive,staticcheck // a product name
		}
		qs, _ := gitQuote(githubURL)
		lines = append(lines, "[credential "+qs+"]", gitIndent+"helper =", gitIndent+"helper = "+q)
	}
	ordered := make([]*gitBlock, 0, len(blocks))
	for _, b := range blocks {
		ordered = append(ordered, b)
	}
	slices.SortFunc(ordered, func(a, b *gitBlock) int {
		if a.depth != b.depth {
			return a.depth - b.depth
		}
		return strings.Compare(a.key, b.key)
	})
	conds := []string{"gitdir/i:"}
	if g.worktree {
		conds = append(conds, "worktree/i:")
	}
	for _, cond := range conds {
		for _, b := range ordered {
			pat, err := gitdirPattern(b.folder)
			if err != nil {
				return plan, err
			}
			q, err := gitQuote(cond + pat)
			if err != nil {
				return plan, fmt.Errorf("the folder %s: %w", b.folder, err)
			}
			lines = append(lines, "[includeIf "+q+"]")
			for _, p := range b.paths {
				qp, _ := gitQuote(p)
				lines = append(lines, gitIndent+"path = "+qp)
			}
		}
	}
	if len(lines) > len(head) {
		want[gitRulesFile] = sealGitFile(lines)
	}

	// Devpit's own files, in a fixed order: identities, default, SSH, then
	// rules.gitconfig (which includes them) and devpit.json.
	names := make([]string, 0, len(want))
	for n := range want {
		if n != gitRulesFile {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	if _, ok := want[gitRulesFile]; ok {
		names = append(names, gitRulesFile)
	}
	var ops []gitOp
	for _, n := range names {
		path := filepath.Join(g.dir, n)
		cur, st, err := readGitFile(path)
		if err != nil {
			return plan, err
		}
		switch st {
		case gitFileForeign:
			return plan, fmt.Errorf("%s is in Devpit's Git folder but Devpit did not write it. Move it somewhere else and try again", path)
		case gitFileEdited:
			return plan, editedError(path)
		case gitFileOurs:
			if bytes.Equal(cur, want[n]) {
				continue
			}
		}
		ops = append(ops, gitOp{path: path, data: want[n], before: cur, exists: st != gitFileMissing, what: "Devpit's Git file " + n})
	}
	// Devpit's files that are no longer needed.
	if entries, err := os.ReadDir(g.dir); err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(n), ".gitconfig") {
				continue
			}
			if _, keep := want[strings.ToLower(n)]; keep {
				continue
			}
			path := filepath.Join(g.dir, n)
			cur, st, err := readGitFile(path)
			if err != nil {
				return plan, err
			}
			switch st {
			case gitFileEdited:
				return plan, editedError(path)
			case gitFileOurs:
				ops = append(ops, gitOp{path: path, before: cur, exists: true, what: "Devpit's Git file " + n})
			}
		}
	}
	// devpit.json.
	extrasPath := filepath.Join(g.dir, gitExtrasFile)
	curX, err := os.ReadFile(extrasPath) // #nosec G304 -- Devpit's own file
	hasX := err == nil
	switch {
	case x.empty() && hasX:
		ops = append(ops, gitOp{path: extrasPath, before: curX, exists: true, what: "Devpit's Git settings"})
	case !x.empty() && !bytes.Equal(curX, x.encode()):
		ops = append(ops, gitOp{path: extrasPath, data: x.encode(), before: curX, exists: hasX, what: "Devpit's Git settings"})
	}

	// The include line in the person's global config.
	_, wantInclude := want[gitRulesFile]
	inc, err := g.includeStatus(ctx)
	if err != nil {
		return plan, err
	}
	switch {
	case wantInclude && !inc.present:
		op, err := g.addInclude(ctx, inc)
		if err != nil {
			return plan, err
		}
		ops = append(ops, op)
	case !wantInclude && inc.present:
		op, err := g.removeInclude(ctx, inc)
		if err != nil {
			return plan, err
		}
		ops = append([]gitOp{op}, ops...)
	case wantInclude && len(inc.after) > 0:
		plan.warnings = append(plan.warnings, fmt.Sprintf(
			"In %s, these lines come after Devpit's include and win over its rules: %s. Move Devpit's [include] block to the end of the file.",
			displayPath(g.d.Home, g.global), strings.Join(inc.after, ", ")))
	}
	plan.ops = ops
	return plan, nil
}

// apply makes the plan's changes through txn, each recorded before it is
// made, then asks Git to read the result.
func (g gitSync) apply(ctx context.Context, txn *accounts.Txn, plan gitPlan, emit func(accounts.Event)) error {
	if len(plan.ops) == 0 {
		emitStep(emit, "Devpit's Git files are already up to date", accounts.StepDone, "")
		return nil
	}
	if err := txn.MakeDir(g.dir, "Devpit's Git folder"); err != nil {
		return err
	}
	for _, o := range plan.ops {
		step := "Writing " + displayPath(g.d.Home, o.path)
		switch {
		case o.global:
			step = "Updating the include line in " + displayPath(g.d.Home, o.path)
		case o.remove():
			step = "Removing " + displayPath(g.d.Home, o.path)
		}
		emitStep(emit, step, accounts.StepRunning, "")
		var err error
		if o.remove() {
			err = removeFileTxn(txn, o)
		} else {
			err = txn.WriteFile(o.path, o.what, o.data)
		}
		if err != nil {
			return err
		}
		emitStep(emit, step, accounts.StepDone, "")
	}
	emitStep(emit, "Checking that Git reads the new config", accounts.StepRunning, "")
	res, err := g.d.run(ctx, g.neutral(Cmd{Name: "git", Args: []string{"config", "--global", "--includes", "--list"}}))
	if err != nil {
		return accounts.ScrubError(err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("Git cannot read its config after the change, so Devpit is putting it back: %s", firstOf(FirstLine(res.Stderr), FirstLine(res.Stdout))) //nolint:revive,staticcheck // a product name
	}
	emitStep(emit, "Checking that Git reads the new config", accounts.StepDone, "")
	return nil
}

func emitStep(emit func(accounts.Event), step string, st accounts.StepState, detail string) {
	if emit != nil {
		emit(accounts.NewEvent(step, st, detail))
	}
}

// removeFileTxn removes a file as a journalled effect: undo writes it back.
func removeFileTxn(txn *accounts.Txn, o gitOp) error {
	sum := sha256.Sum256(o.before)
	eff := accounts.Effect{
		Kind: accounts.EffectFile, Target: o.path, Summary: "Removed " + o.what,
		BeforeExists: true, Before: o.before, BeforeHash: hex.EncodeToString(sum[:]),
	}
	return txn.Do(eff, func() (accounts.Effect, error) {
		if err := os.Remove(o.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return eff, err
		}
		eff.AfterHash = ""
		return eff, nil
	})
}

// sameEdits reports whether two lists of edits are identical.
func sameEdits(a, b []accounts.FileEdit) bool {
	return slices.EqualFunc(a, b, func(x, y accounts.FileEdit) bool {
		return x.Path == y.Path && x.Action == y.Action && slices.Equal(x.Lines, y.Lines)
	})
}

// neutral runs a Git command outside any repository, so a conditional
// include cannot change the answer: in the temp folder, with Git told not
// to look above it, and with no repository variables inherited.
func (g gitSync) neutral(c Cmd) Cmd { return neutralGit(c) }

// inDirOf runs a `git config --file <name>` command in the file's own
// folder, so the path is not on the command line (a long temp folder name
// can look like a token to CheckArgs).
func (g gitSync) inDirOf(path string, c Cmd) Cmd {
	c = neutralGit(c)
	c.Dir = filepath.Dir(path)
	return c
}

func neutralGit(c Cmd) Cmd {
	tmp := os.TempDir()
	c.Dir = tmp
	c.Env = append(c.Env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(tmp))
	c.Unset = append(c.Unset, "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR")
	return c
}

// pathInside reports whether p is dir or inside it (case-insensitive on
// Windows, either slash).
func pathInside(dir, p string) bool {
	if dir == "" || p == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(filepath.FromSlash(dir)), filepath.Clean(filepath.FromSlash(p)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// zRecords splits `git config -z` output into its NUL-terminated records.
func zRecords(out []byte) []string {
	parts := strings.Split(string(out), "\x00")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// originPath turns a --show-origin value ("file:C:/x/y") into a path.
func originPath(origin string) (string, bool) {
	p, ok := strings.CutPrefix(origin, "file:")
	return p, ok
}

// previousHelpers lists the credential helpers Git uses for
// https://github.com without Devpit: system and global config, in order,
// with an empty value resetting the list as Git does. Lines from Devpit's
// own files are left out.
func (g gitSync) previousHelpers(ctx context.Context) ([]string, error) {
	res, err := g.d.run(ctx, g.neutral(Cmd{Name: "git", Args: []string{
		"config", "--show-origin", "--show-scope", "-z", "--get-regexp", `^credential\..*helper$`,
	}}))
	if err != nil {
		return nil, accounts.ScrubError(err)
	}
	if res.ExitCode == 1 {
		return nil, nil
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("Git could not list its credential helpers: %s", FirstLine(res.Stderr)) //nolint:revive,staticcheck // a product name
	}
	return parseHelperChain(zRecords(res.Stdout), g.dir), nil
}

// parseHelperChain applies the records of `git config --show-origin
// --show-scope -z --get-regexp` (scope, origin, "key\nvalue") in order.
func parseHelperChain(recs []string, devpitDir string) []string {
	var chain []string
	for i := 0; i+2 < len(recs); i += 3 {
		scope, origin, kv := recs[i], recs[i+1], recs[i+2]
		if scope != "system" && scope != "global" {
			continue
		}
		if p, ok := originPath(origin); ok && pathInside(devpitDir, p) {
			continue
		}
		key, val, _ := strings.Cut(kv, "\n")
		if !helperKeyMatchesGitHub(key) {
			continue
		}
		if val == "" {
			chain = nil
			continue
		}
		chain = append(chain, val)
	}
	return chain
}

// helperKeyMatchesGitHub reports whether a credential.*helper key applies
// to https://github.com with no user name and no path, which is what Git
// asks for on a push.
func helperKeyMatchesGitHub(key string) bool {
	low := strings.ToLower(key)
	if low == "credential.helper" {
		return true
	}
	if !strings.HasPrefix(low, "credential.") || !strings.HasSuffix(low, ".helper") {
		return false
	}
	u := key[len("credential.") : len(key)-len(".helper")]
	u = strings.TrimSuffix(u, "/")
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		rest = u
	} else if !strings.EqualFold(scheme, "https") {
		return false
	}
	if strings.Contains(rest, "@") || strings.Contains(rest, "/") {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(rest), ":443")
	return host == "github.com"
}

// ownIdentity is the person's own user.name and user.email: global config
// with its includes, outside any repository, leaving out Devpit's files.
func (g gitSync) ownIdentity(ctx context.Context) (name, email string, err error) {
	get := func(key string) (string, error) {
		res, rerr := g.d.run(ctx, g.neutral(Cmd{Name: "git", Args: []string{"config", "--global", "--includes", "--show-origin", "-z", "--get-all", key}}))
		if rerr != nil {
			return "", accounts.ScrubError(rerr)
		}
		if res.ExitCode == 1 {
			return "", nil
		}
		if res.ExitCode != 0 {
			return "", fmt.Errorf("Git could not read your %s: %s", key, FirstLine(res.Stderr)) //nolint:revive,staticcheck // a product name
		}
		recs := zRecords(res.Stdout)
		val := ""
		for i := 0; i+1 < len(recs); i += 2 {
			if p, ok := originPath(recs[i]); ok && pathInside(g.dir, p) {
				continue
			}
			val = recs[i+1]
		}
		return val, nil
	}
	if name, err = get("user.name"); err != nil {
		return "", "", err
	}
	email, err = get("user.email")
	return name, email, err
}

// includeStatus is where Devpit's include line stands in the global config.
type includeStatus struct {
	raw     []byte
	exists  bool
	present bool
	// spellings are the values of the include lines that point at
	// rules.gitconfig, as written.
	spellings []string
	// after lists the keys after Devpit's include that override its rules.
	after []string
}

func (g gitSync) includeValue() string { return slashPath(filepath.Join(g.dir, gitRulesFile)) }

func sameConfigPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(filepath.FromSlash(a)), filepath.Clean(filepath.FromSlash(b)))
}

// overrides reports whether a key after Devpit's include changes what the
// rules set.
func overrides(key string) bool {
	k := strings.ToLower(key)
	switch k {
	case "user.name", "user.email", "user.signingkey", "gpg.format", "commit.gpgsign", "core.sshcommand", "include.path", "credential.helper":
		return true
	}
	return (strings.HasPrefix(k, "includeif.") && strings.HasSuffix(k, ".path")) ||
		(strings.HasPrefix(k, "credential.") && strings.HasSuffix(k, ".helper"))
}

// readInclude parses `git config --file <f> --no-includes --list -z`.
func (g gitSync) readInclude(ctx context.Context, path string) (includeStatus, error) {
	st := includeStatus{}
	res, err := g.d.run(ctx, g.inDirOf(path, Cmd{Name: "git", Args: []string{"config", "--file", filepath.Base(path), "--no-includes", "--list", "-z"}}))
	if err != nil {
		return st, accounts.ScrubError(err)
	}
	if res.ExitCode != 0 && len(bytes.TrimSpace(res.Stderr)) > 0 {
		return st, fmt.Errorf("Git cannot read %s: %s. Fix that file first", displayPath(g.d.Home, g.global), FirstLine(res.Stderr)) //nolint:revive,staticcheck // a product name
	}
	want := g.includeValue()
	var after []string
	for _, rec := range zRecords(res.Stdout) {
		key, val, _ := strings.Cut(rec, "\n")
		if strings.EqualFold(key, "include.path") && sameConfigPath(val, want) {
			st.present = true
			if !slices.Contains(st.spellings, val) {
				st.spellings = append(st.spellings, val)
			}
			after = nil
			continue
		}
		if st.present && overrides(key) && !slices.Contains(after, strings.ToLower(key)) {
			after = append(after, strings.ToLower(key))
		}
	}
	st.after = after
	return st, nil
}

func (g gitSync) includeStatus(ctx context.Context) (includeStatus, error) {
	raw, err := os.ReadFile(g.global)
	if errors.Is(err, os.ErrNotExist) {
		return includeStatus{}, nil
	}
	if err != nil {
		return includeStatus{}, err
	}
	st, err := g.readInclude(ctx, g.global)
	st.raw, st.exists = raw, true
	return st, err
}

// includeBlock is what Devpit adds at the end of the global config.
func (g gitSync) includeBlock() []string {
	q, _ := gitQuote(g.includeValue())
	return []string{
		"# Added by Devpit: its folder rules for Git and GitHub. Keep this block last; Devpit removes it with the last rule.",
		"[include]",
		gitIndent + "path = " + q,
	}
}

func newline(raw []byte) string {
	if bytes.Contains(raw, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// addInclude appends Devpit's block at the very end of the global config.
// `git config --add include.path` is not used on purpose: Git puts the new
// line inside an [include] section that already exists, wherever it is, so
// lines after it would win over the rules. Git then reads the result back
// before anything is written, to prove it parses and the include is last.
func (g gitSync) addInclude(ctx context.Context, st includeStatus) (gitOp, error) {
	if err := g.checkValue(); err != nil {
		return gitOp{}, err
	}
	if accounts.ContentLooksSecret(string(st.raw)) {
		// The engine would refuse to copy it into the undo history anyway;
		// say what to do instead.
		return gitOp{}, fmt.Errorf("%s holds something that looks like a secret (a token in a URL, a long key id), so Devpit will not copy it into its undo history and will not change it. "+
			"Add these lines at the very end of that file yourself, then try again:\n%s",
			displayPath(g.d.Home, g.global), strings.Join(g.includeBlock(), "\n"))
	}
	nl := newline(st.raw)
	data := slices.Clone(st.raw)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, nl...)
	}
	block := g.includeBlock()
	data = append(data, strings.Join(block, nl)+nl...)
	got, err := g.checkBytes(ctx, data)
	if err != nil {
		return gitOp{}, err
	}
	if !got.present || len(got.after) > 0 {
		return gitOp{}, errors.New("Git did not read Devpit's include as the last line of your global config; nothing was changed") //nolint:revive,staticcheck // a product name
	}
	return gitOp{
		path: g.global, data: data, before: st.raw, exists: st.exists, global: true,
		lines: block, action: "adds", what: "Devpit's include line",
	}, nil
}

// removeInclude takes Devpit's include out of the global config: the block
// exactly as Devpit added it when it is still there, otherwise every
// include.path line naming rules.gitconfig, removed by Git itself.
func (g gitSync) removeInclude(ctx context.Context, st includeStatus) (gitOp, error) {
	if accounts.ContentLooksSecret(string(st.raw)) {
		return gitOp{}, fmt.Errorf("%s holds something that looks like a secret, so Devpit will not change it. Remove Devpit's [include] block (the lines naming %s) yourself, then try again",
			displayPath(g.d.Home, g.global), g.includeValue())
	}
	var data []byte
	var shown []string
	for _, nl := range []string{"\r\n", "\n"} {
		blk := []byte(strings.Join(g.includeBlock(), nl) + nl)
		if i := bytes.Index(st.raw, blk); i >= 0 {
			data = append(slices.Clone(st.raw[:i]), st.raw[i+len(blk):]...)
			shown = g.includeBlock()
			break
		}
	}
	if data == nil {
		tmp, err := tempCopy(st.raw)
		if err != nil {
			return gitOp{}, err
		}
		defer removeTemp(tmp)
		for _, sp := range st.spellings {
			res, rerr := g.d.run(ctx, g.inDirOf(tmp, Cmd{Name: "git", Args: []string{"config", "--file", filepath.Base(tmp), "--fixed-value", "--unset-all", "include.path", sp}}))
			if rerr != nil {
				return gitOp{}, accounts.ScrubError(rerr)
			}
			if res.ExitCode != 0 {
				return gitOp{}, fmt.Errorf("Git could not remove Devpit's include line from %s: %s", displayPath(g.d.Home, g.global), FirstLine(res.Stderr)) //nolint:revive,staticcheck // a product name
			}
			q, _ := gitQuote(sp)
			shown = append(shown, "[include] path = "+q)
		}
		if data, err = os.ReadFile(tmp); err != nil { // #nosec G304 -- our temp copy
			return gitOp{}, err
		}
	}
	got, err := g.checkBytes(ctx, data)
	if err != nil {
		return gitOp{}, err
	}
	if got.present {
		return gitOp{}, errors.New("Devpit's include line is still in your global config after removing it; nothing was changed") //nolint:revive,staticcheck // a product name
	}
	return gitOp{
		path: g.global, data: nonNil(data), before: st.raw, exists: true, global: true,
		lines: shown, action: "removes", what: "Devpit's include line",
	}, nil
}

// nonNil keeps an emptied global config a write of zero bytes, not a
// removal: Devpit never deletes the person's own file.
func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func (g gitSync) checkValue() error {
	_, err := gitQuote(g.includeValue())
	return err
}

// checkBytes has Git parse data as a config file and returns where the
// include stands in it.
func (g gitSync) checkBytes(ctx context.Context, data []byte) (includeStatus, error) {
	tmp, err := tempCopy(data)
	if err != nil {
		return includeStatus{}, err
	}
	defer removeTemp(tmp)
	return g.readInclude(ctx, tmp)
}

// removeTemp removes a temp file tempCopy made.
func removeTemp(name string) {
	_ = os.Remove(name) // #nosec G703 -- a file os.CreateTemp named for us
}

func tempCopy(data []byte) (string, error) {
	f, err := os.CreateTemp("", "devpit-gitconfig-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
