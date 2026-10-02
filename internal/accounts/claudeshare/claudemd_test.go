package claudeshare

import (
	"bytes"
	"strings"
	"testing"
)

func TestImportLineIsAbsoluteForwardSlashedAndEscaped(t *testing.T) {
	got := importLine(`C:\Users\Zubair Shaukat\.claude\CLAUDE.md`)
	if got != `@C:/Users/Zubair\ Shaukat/.claude/CLAUDE.md` {
		t.Fatalf("import line = %q", got)
	}
}

func TestImportBlockRoundTripsExactly(t *testing.T) {
	src := `C:\Users\me\.claude\CLAUDE.md`
	cases := map[string][]byte{
		"empty":     nil,
		"lf":        []byte("# Work\nUse tabs.\n"),
		"crlf":      []byte("# Work\r\nUse tabs.\r\n"),
		"bom":       append(append([]byte(nil), utf8BOM...), []byte("# Work\n")...),
		"no-eol":    []byte("one line"),
		"unicode":   []byte("Привет — ünïcödé\n"),
		"has-marks": []byte("<!-- a comment of my own -->\n"),
	}
	for name, content := range cases {
		out, ins := addImport(content, src)
		ok, path := hasImportBlock(out)
		if !ok || !samePlace(path, src) && !strings.EqualFold(strings.ReplaceAll(path, `\`, "/"), strings.ReplaceAll(src, `\`, "/")) {
			t.Errorf("%s: block not found (%v %q)", name, ok, path)
		}
		if len(content) > 0 && !bytes.HasSuffix(out, bytes.TrimPrefix(content, utf8BOM)) {
			t.Errorf("%s: own text is not kept below: %q", name, out)
		}
		if bytes.HasPrefix(content, utf8BOM) && !bytes.HasPrefix(out, utf8BOM) {
			t.Errorf("%s: BOM lost", name)
		}
		if strings.Contains(string(content), "\r\n") && strings.Contains(strings.ReplaceAll(ins, "\r\n", ""), "\n") {
			t.Errorf("%s: mixed line endings: %q", name, ins)
		}
		back, ok := removeImport(out, ins)
		if !ok || !bytes.Equal(back, content) {
			t.Errorf("%s: undo = %q, want %q", name, back, content)
		}
		body := withoutBlock(out)
		if !bytes.Equal(bytes.TrimPrefix(body, utf8BOM), bytes.TrimPrefix(content, utf8BOM)) {
			t.Errorf("%s: withoutBlock = %q", name, body)
		}
	}
}
