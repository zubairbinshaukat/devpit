package launch

import (
	"bufio"
	"bytes"
	"strings"
)

// parseNpmShim recognises the .cmd file npm writes for a package's binary
// (the cmd-shim package) and returns the arguments that come before the
// person's own: normally just the node script. dir is the .cmd's own folder,
// what %dp0% and %~dp0 stand for. ok is false for anything else, which is
// then treated as an ordinary batch file.
//
// Two layouts are recognised, the current one:
//
//	endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\vercel\dist\vc.js" %*
//
// and the older one:
//
//	"%~dp0\node.exe"  "%~dp0\node_modules\npm\bin\npm-cli.js" %*
//
// Every token between the program and %* must expand to plain text once
// %dp0% / %~dp0 is replaced; a token with any other % is not understood, and
// the file is not treated as an npm shim.
func parseNpmShim(content []byte, dir string) ([]string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasSuffix(line, "%*") {
			continue
		}
		var rest string
		low := strings.ToLower(line)
		switch {
		case strings.Contains(low, `"%_prog%"`):
			i := strings.Index(low, `"%_prog%"`)
			rest = line[i+len(`"%_prog%"`):]
		case strings.HasPrefix(low, `"%~dp0\node.exe"`):
			rest = line[len(`"%~dp0\node.exe"`):]
		default:
			continue
		}
		rest = strings.TrimSpace(strings.TrimSuffix(rest, "%*"))
		toks, ok := splitCmdTokens(rest)
		if !ok || len(toks) == 0 {
			return nil, false
		}
		base := strings.TrimRight(dir, `\/`) + `\`
		out := make([]string, 0, len(toks))
		for _, t := range toks {
			t = replaceFold(t, "%dp0%", base)
			t = replaceFold(t, "%~dp0", base)
			if strings.ContainsAny(t, "%!^&|<>") {
				return nil, false
			}
			out = append(out, cleanSlashes(t))
		}
		return out, true
	}
	return nil, false
}

// splitCmdTokens splits a cmd.exe argument list made only of plain and
// double-quoted tokens.
func splitCmdTokens(s string) ([]string, bool) {
	var out []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return out, true
		}
		if s[0] == '"' {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				return nil, false
			}
			out = append(out, s[1:1+end])
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t")
		if end < 0 {
			end = len(s)
		}
		tok := s[:end]
		if strings.ContainsRune(tok, '"') {
			return nil, false
		}
		out = append(out, tok)
		s = s[end:]
	}
}

// replaceFold replaces every case-insensitive occurrence of old.
func replaceFold(s, old, repl string) string {
	low, lold := strings.ToLower(s), strings.ToLower(old)
	var b strings.Builder
	for {
		i := strings.Index(low, lold)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, low = s[i+len(old):], low[i+len(old):]
	}
}

// cleanSlashes collapses the doubled backslash "%dp0%\x" leaves behind
// (%dp0% already ends with one), except a leading \\ of a UNC path.
func cleanSlashes(p string) string {
	prefix := ""
	if strings.HasPrefix(p, `\\`) {
		prefix, p = `\\`, p[2:]
	}
	for strings.Contains(p, `\\`) {
		p = strings.ReplaceAll(p, `\\`, `\`)
	}
	return prefix + p
}
