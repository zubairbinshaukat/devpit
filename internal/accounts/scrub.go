package accounts

import "strings"

// Hidden replaces anything [Scrub] removes.
const Hidden = "[hidden]"

// Scrub returns s with anything that looks like a token, key or password
// replaced by [Hidden]. Every line of tool output that reaches an event, a
// log line, an error or a returned struct goes through it first, and the
// store refuses any value that Scrub would change.
//
// It looks for three shapes, without regular expressions so the shim stays
// fast to start:
//
//   - a run of token characters starting with a known token prefix
//     (ghp_, gho_, github_pat_, sk-, sbp_, ya29., xoxb-, glpat-, AKIA, eyJ…);
//   - any run of 32 or more token characters that mixes letters and digits
//     (base64 and hex secrets, JWT parts, OAuth refresh tokens);
//   - the value after a key that names a secret ("token", "password",
//     "secret", "api_key", "authorization"…) and a ':' or '=', including a
//     whole quoted value, and the value after "Bearer" or "Basic".
//
// This is the strict rule, for free text: tool output, errors, events and
// log lines. Values Devpit knows are file system paths are checked with
// [PathLooksSecret] instead, and text Devpit writes into its own config
// files with [ContentLooksSecret], because a folder name may well be a long
// mix of letters and digits (a temp folder, a hashed build folder, a user
// profile) without being a secret.
func Scrub(s string) string { return scrub(s, false) }

// ScrubKeepingPaths is [Scrub] for a sentence Devpit itself built that
// names folders (a journal summary): the long-and-mixed rule is not applied
// inside an absolute path (C:\…, C:/…, \\server\share\…), every other rule
// is, and a known token prefix is hidden even inside a path.
func ScrubKeepingPaths(s string) string { return scrub(s, true) }

// scrub is Scrub; with paths set, runs inside an absolute path are judged by
// the known-prefix rule only.
func scrub(s string, paths bool) string {
	if s == "" {
		return s
	}
	var inPath []bool
	if paths {
		inPath = pathSpans(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	var (
		pendingKey bool // the last run named a secret
		keyBearer  bool // ...and it was "Bearer" or "Basic", so a space is enough
		sawAssign  bool // a ':' or '=' followed that key
	)
	reset := func() { pendingKey, keyBearer, sawAssign = false, false, false }

	i := 0
	for i < len(s) {
		c := s[i]
		if !isTokenByte(c) {
			if pendingKey {
				switch {
				case c == ':' || c == '=':
					sawAssign = true
				case (c == '"' || c == '\'') && sawAssign:
					if end := strings.IndexByte(s[i+1:], c); end >= 0 {
						b.WriteByte(c)
						if end > 0 {
							b.WriteString(Hidden)
						}
						b.WriteByte(c)
						i += end + 2
						reset()
						continue
					}
				case c == ' ' || c == '\t' || c == '"' || c == '\'':
				default:
					reset()
				}
			}
			b.WriteByte(c)
			i++
			continue
		}

		j := i
		for j < len(s) && isTokenByte(s[j]) {
			j++
		}
		run := s[i:j]
		low := strings.ToLower(run)
		switch {
		case pendingKey && sawAssign && (low == "bearer" || low == "basic" || low == "token"):
			// "Authorization: Bearer x": the scheme is not the secret.
			b.WriteString(run)
			keyBearer, sawAssign = true, false
		case pendingKey && (sawAssign || keyBearer) && len(run) >= 6:
			b.WriteString(Hidden)
			reset()
		case knownPrefix(run) || (secretRun(run) && (inPath == nil || !inPath[i])):
			b.WriteString(Hidden)
			reset()
		default:
			b.WriteString(run)
			keyBearer = low == "bearer" || low == "basic"
			pendingKey = keyBearer || secretKey(low)
			sawAssign = false
		}
		i = j
	}
	return b.String()
}

// LooksSecret reports whether s holds anything [Scrub] would hide. It is the
// strict rule, for free text and tool output.
func LooksSecret(s string) bool {
	return Scrub(s) != s
}

// PathLooksSecret is the check for a value Devpit knows is a file system
// path (a folder rule, an account folder, a path argument, a file target)
// or another identifier Devpit made itself (a profile name, a hash): only a
// well-known token prefix counts. A long, random-looking folder name is a
// folder name.
func PathLooksSecret(p string) bool { return hasKnownToken(p) }

// ContentLooksSecret is the check for text Devpit writes into, or reads from,
// a config file it changes (a Git include file, the person's global Git
// config): the strict rule everywhere except inside absolute paths, where a
// long mixed run is a folder name and only a known token prefix counts. A
// token after "token=" or in a URL is still caught.
func ContentLooksSecret(s string) bool { return ScrubKeepingPaths(s) != s }

// IsAbsPathArg reports whether an argument is an absolute Windows path
// (C:\…, C:/…, \\server\share), or a --flag=<such a path>, and returns the
// path part. Runners check such an argument with [PathLooksSecret].
func IsAbsPathArg(a string) (string, bool) {
	if strings.HasPrefix(a, "-") {
		_, v, ok := strings.Cut(a, "=")
		if !ok {
			return "", false
		}
		a = v
	}
	if pathStart(a, 0) {
		return a, true
	}
	return "", false
}

// pathStart reports whether an absolute path starts at s[i]: a drive letter,
// a colon and a slash, or two backslashes (a share), with no letter or digit
// right before it.
func pathStart(s string, i int) bool {
	if i > 0 {
		p := s[i-1]
		if (p >= 'a' && p <= 'z') || (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') || p == '\\' {
			return false
		}
	}
	if i+2 < len(s) && ((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) && s[i+1] == ':' && (s[i+2] == '\\' || s[i+2] == '/') {
		return true
	}
	return i+2 < len(s) && s[i] == '\\' && s[i+1] == '\\' && s[i+2] != '\\'
}

// pathSpans marks every byte of s that is part of an absolute path. A path
// runs from its start to a character no Windows path can hold (a quote, <,
// >, |, *, ?, a control character), or to a space after which the next word
// holds no slash: "C:\Users\John Smith\x" is one path, while in "C:\x and
// more" the path ends before " and".
func pathSpans(s string) []bool {
	out := make([]bool, len(s))
	for i := 0; i < len(s); i++ {
		if !pathStart(s, i) {
			continue
		}
		j := i
		for j < len(s) && !pathStop(s[j]) {
			if s[j] == ' ' && !slashInNextWord(s, j+1) {
				break
			}
			out[j] = true
			j++
		}
		i = j
	}
	return out
}

// slashInNextWord reports whether the word starting at s[i] (up to a space
// or a stop character) holds a path separator.
func slashInNextWord(s string, i int) bool {
	for ; i < len(s) && s[i] != ' ' && !pathStop(s[i]); i++ {
		if s[i] == '\\' || s[i] == '/' {
			return true
		}
	}
	return false
}

func pathStop(c byte) bool {
	switch c {
	case '"', '\'', '<', '>', '|', '*', '?':
		return true
	}
	return c < ' ' || c == 0x7f
}

// isTokenByte is the alphabet tokens are written in. '/', '+' and '=' are
// left out on purpose: they split base64 into pieces, each still long enough
// to be caught, and keeping them out leaves paths and URLs readable.
func isTokenByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
		c == '_' || c == '-' || c == '.' || c == '~'
}

// secretRun reports whether one run of token characters is a secret on its
// own: a well-known credential prefix with enough after it, or long and
// mixed.
func secretRun(run string) bool {
	if knownPrefix(run) {
		return true
	}
	if len(run) < 32 {
		return false
	}
	letters, digits := false, false
	for i := 0; i < len(run); i++ {
		c := run[i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letters = true
		}
	}
	return letters && digits
}

// knownPrefix reports whether a run starts with a well-known credential
// prefix and has enough after it to be one.
func knownPrefix(run string) bool {
	prefixes := [...]string{
		"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_",
		"sk-", "sk_live_", "sk_test_", "rk_live_", "sbp_",
		"ya29.", "xoxb-", "xoxp-", "xoxa-", "xoxs-", "glpat-", "AKIA", "ASIA",
		"eyJ", "npm_", "pypi-", "vcp_", "dop_v1_", "hf_",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(run, p) && len(run) >= len(p)+12 {
			return true
		}
	}
	return false
}

// secretKey reports whether a run, in lower case, names a secret, so the
// value after it is hidden.
func secretKey(low string) bool {
	keys := [...]string{
		"token", "secret", "password", "passwd", "pwd", "apikey", "api_key", "api-key",
		"authorization", "bearer", "credential", "cookie", "session_key",
		"private_key", "privatekey", "access_key",
	}
	for _, k := range keys {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// hasKnownToken is the check for folder paths: only well-known credential
// prefixes count, since a folder name may well be a long mix of letters and
// digits (a build folder, a temp folder) without being a secret.
func hasKnownToken(s string) bool {
	for i := 0; i < len(s); {
		if !isTokenByte(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && isTokenByte(s[j]) {
			j++
		}
		if knownPrefix(s[i:j]) {
			return true
		}
		i = j
	}
	return false
}
