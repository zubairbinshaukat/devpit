package claudeshare

import (
	"bytes"
	"path/filepath"
	"strings"
)

// CLAUDE.md is shared with one managed import line: the account's CLAUDE.md
// starts with a block, between two marker comments, holding
// "@C:/Users/<you>/.claude/CLAUDE.md". Claude Code loads a user-scope
// file's absolute imports without asking (tested on 2.1.287, spikes §10).
// The account's own content, if any, stays below the block. No file link
// (it would need privileges) and no hard link (editors break it).

const (
	mdBegin = "<!-- devpit:shared-claude-md: the line below brings in the default account's CLAUDE.md. Devpit manages these three lines. -->"
	mdEnd   = "<!-- devpit:end -->"
)

// importLine is the `@` line for path: forward slashes, spaces escaped with
// a backslash, never quoted (a quoted path is not imported).
func importLine(path string) string {
	p := filepath.ToSlash(path)
	p = strings.ReplaceAll(p, `\`, "/")
	return "@" + strings.ReplaceAll(p, " ", `\ `)
}

func importBlock(src, nl string) string {
	return mdBegin + nl + importLine(src) + nl + mdEnd + nl
}

func lineEnding(b []byte) string {
	if bytes.Contains(b, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// addImport returns content with the block in front (after a byte-order
// mark), and the exact text inserted, which undo removes again.
func addImport(content []byte, src string) ([]byte, string) {
	bom := bytes.HasPrefix(content, utf8BOM)
	rest := content
	if bom {
		rest = content[len(utf8BOM):]
	}
	nl := lineEnding(rest)
	ins := importBlock(src, nl)
	if len(rest) > 0 {
		ins += nl
	}
	out := make([]byte, 0, len(content)+len(ins))
	if bom {
		out = append(out, utf8BOM...)
	}
	out = append(out, ins...)
	out = append(out, rest...)
	return out, ins
}

// hasImportBlock reports whether content holds Devpit's block, and the
// import path in it.
func hasImportBlock(content []byte) (bool, string) {
	s := string(content)
	i := strings.Index(s, mdBegin)
	if i < 0 {
		return false, ""
	}
	j := strings.Index(s[i:], mdEnd)
	if j < 0 {
		return false, ""
	}
	inner := strings.TrimSpace(s[i+len(mdBegin) : i+j])
	path := strings.TrimPrefix(inner, "@")
	path = strings.ReplaceAll(path, `\ `, " ")
	return true, filepath.FromSlash(path)
}

// removeImport removes the exact inserted text. ok is false when it is not
// there as written.
func removeImport(content []byte, inserted string) ([]byte, bool) {
	i := bytes.Index(content, []byte(inserted))
	if i < 0 {
		return content, false
	}
	out := append([]byte(nil), content[:i]...)
	return append(out, content[i+len(inserted):]...), true
}

// withoutBlock returns content with Devpit's block (and the blank line after
// it) taken out, for turning a shared CLAUDE.md into the account's own.
func withoutBlock(content []byte) []byte {
	s := string(content)
	i := strings.Index(s, mdBegin)
	if i < 0 {
		return content
	}
	j := strings.Index(s[i:], mdEnd)
	if j < 0 {
		return content
	}
	end := i + j + len(mdEnd)
	for _, nl := range []string{"\r\n\r\n", "\n\n", "\r\n", "\n"} {
		if strings.HasPrefix(s[end:], nl) {
			end += len(nl)
			break
		}
	}
	return []byte(s[:i] + s[end:])
}
