package claudeshare

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A .claude.json as Claude Code writes it: identity, per-project state and
// the user-scope MCP servers.
const targetClaudeJSON = "{\r\n" +
	"  \"numStartups\": 12,\r\n" +
	"  \"oauthAccount\": {\r\n" +
	"    \"emailAddress\": \"work@example.com\",\r\n" +
	"    \"accountUuid\": \"0000-1111\"\r\n" +
	"  },\r\n" +
	"  \"projects\": {\r\n" +
	"    \"C:/work/app\": {\r\n" +
	"      \"allowedTools\": [],\r\n" +
	"      \"mcpServers\": {}\r\n" +
	"    }\r\n" +
	"  },\r\n" +
	"  \"mcpServers\": {\r\n" +
	"    \"files\": {\r\n" +
	"      \"command\": \"old-files\"\r\n" +
	"    }\r\n" +
	"  },\r\n" +
	"  \"tipsHistory\": {\"a\": 1}\r\n" +
	"}\r\n"

const sourceClaudeJSON = `{
  "oauthAccount": {"emailAddress": "me@example.com"},
  "mcpServers": {
    "github": {
      "command": "gh-mcp",
      "env": {"GITHUB_PAT": "not-a-real-value"}
    },
    "files": {
      "command": "new-files"
    }
  }
}
`

func decodeAny(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	must(t, json.Unmarshal(bytes.TrimPrefix(b, utf8BOM), &m))
	return m
}

// TestMergeLeavesEveryOtherKeyByteForByte pins the promise about
// .claude.json: everything outside mcpServers keeps its exact bytes (not
// just its meaning), because hujson edits the parsed tree in place and
// packs untouched members back verbatim. Undo restores the whole file
// byte for byte.
func TestMergeLeavesEveryOtherKeyByteForByte(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "work", ".claude.json"), filepath.Join(dir, "home", ".claude.json")
	put(t, dst, targetClaudeJSON)
	put(t, src, sourceClaudeJSON)
	out, rec, side, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{
		{Path: []string{"mcpServers", "github"}, Action: ActAdd},
		{Path: []string{"mcpServers", "files"}, Action: ActAddAs, NewName: "files-from-default"},
	}})
	must(t, err)
	if side != nil {
		t.Fatal("nothing was replaced, so nothing should be kept aside")
	}
	orig := []byte(targetClaudeJSON)
	i := bytes.Index(orig, []byte("  \"mcpServers\": {\r\n    \"files\""))
	j := bytes.Index(orig, []byte("  \"tipsHistory\""))
	if !bytes.HasPrefix(out, orig[:i]) || !bytes.HasSuffix(out, orig[j:]) {
		t.Fatalf("bytes outside mcpServers changed:\n%s", out)
	}
	before, after := decodeAny(t, orig), decodeAny(t, out)
	servers := after["mcpServers"].(map[string]any)
	if servers["files"].(map[string]any)["command"] != "old-files" ||
		servers["files-from-default"].(map[string]any)["command"] != "new-files" ||
		servers["github"] == nil {
		t.Fatalf("servers = %v", servers)
	}
	delete(before, "mcpServers")
	delete(after, "mcpServers")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("other keys changed meaning")
	}
	if bytes.Contains(out, []byte("me@example.com")) {
		t.Fatal("the default account's identity reached the account's file")
	}
	if !bytes.Contains(out, []byte("\r\n    \"github\": {")) {
		t.Fatalf("the new member is not spaced like its siblings:\n%s", out)
	}
	recJSON, _ := json.Marshal(rec)
	if bytes.Contains(recJSON, []byte("not-a-real-value")) || bytes.Contains(recJSON, []byte("gh-mcp")) {
		t.Fatalf("the undo record holds a value: %s", recJSON)
	}

	must(t, writeAtomic(dst, out))
	st, err := jsonState(rec)
	must(t, err)
	if st != stAfter {
		t.Fatalf("state = %d", st)
	}
	back, remove, err := revertMerge(rec)
	must(t, err)
	if remove || !bytes.Equal(back, orig) {
		t.Fatalf("undo is not byte for byte:\n%q\n%q", back, orig)
	}
}

func TestMergeRefusesAnythingButMCPServersInClaudeJSON(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "w", ".claude.json"), filepath.Join(dir, "h", ".claude.json")
	put(t, dst, targetClaudeJSON)
	put(t, src, sourceClaudeJSON)
	for _, p := range [][]string{{"oauthAccount"}, {"mcpServers"}, {"projects", "C:/work/app"}, {"oauthAccount", "emailAddress"}} {
		if _, _, _, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{{Path: p, Action: ActAdd}}}); err == nil {
			t.Errorf("%v allowed", p)
		}
	}
	if _, _, _, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{
		{Path: []string{"mcpServers", "github"}, Action: ActAddAs, NewName: "x/../oauthAccount"},
	}}); err != nil {
		// A strange name stays a name under mcpServers; this is allowed.
		t.Errorf("a new name under mcpServers: %v", err)
	}
}

func TestUndoKeepsWhatClaudeCodeWroteSince(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "w", ".claude.json"), filepath.Join(dir, "h", ".claude.json")
	put(t, dst, targetClaudeJSON)
	put(t, src, sourceClaudeJSON)
	out, rec, _, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{{Path: []string{"mcpServers", "github"}, Action: ActAdd}}})
	must(t, err)
	must(t, writeAtomic(dst, out))
	// Claude Code rewrites the file (another startup) without touching our
	// server.
	put(t, dst, strings.Replace(string(out), "\"numStartups\": 12", "\"numStartups\": 13", 1))
	st, err := jsonState(rec)
	must(t, err)
	if st != stAfter {
		t.Fatalf("an unrelated rewrite must not block undo: state %d", st)
	}
	back, _, err := revertMerge(rec)
	must(t, err)
	m := decodeAny(t, back)
	if m["numStartups"].(float64) != 13 || m["mcpServers"].(map[string]any)["github"] != nil {
		t.Fatalf("after undo: %v", m)
	}
	// If the person changed the server Devpit added, undo stops.
	put(t, dst, strings.Replace(string(out), "gh-mcp", "gh-mcp-v2", 1))
	if st, _ := jsonState(rec); st != stChanged {
		t.Fatalf("a changed server must count as changed: %d", st)
	}
}

func TestReplaceKeepsTheOldValueAsideAndUndoPutsItBack(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "w", "settings.json"), filepath.Join(dir, "h", "settings.json")
	orig := "{\n  \"model\": \"sonnet\",\n  \"theme\": \"dark\"\n}\n"
	put(t, dst, orig)
	put(t, src, "{\n  \"model\": \"opus\"\n}\n")
	side := filepath.Join(dir, "w", "backup", "replaced-settings.json")
	out, rec, sideBytes, err := prepareMerge(JSONMerge{
		File: dst, Source: src, Sidecar: side,
		Ops: []MemberOp{{Path: []string{"model"}, Action: ActReplace}},
	})
	must(t, err)
	if string(out) != "{\n  \"model\": \"opus\",\n  \"theme\": \"dark\"\n}\n" {
		t.Fatalf("out = %q", out)
	}
	put(t, side, string(sideBytes))
	must(t, writeAtomic(dst, out))
	back, _, err := revertMerge(rec)
	must(t, err)
	if string(back) != orig {
		t.Fatalf("undo = %q", back)
	}
}

func TestMergeIntoAMissingFileAndUndoRemovesIt(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "w", "settings.json"), filepath.Join(dir, "h", "settings.json")
	put(t, src, "{\n  \"enabledPlugins\": {\"fmt@tools\": true},\n  \"model\": \"opus\"\n}\n")
	put(t, filepath.Join(dir, "w", "keep"), "")
	out, rec, _, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{
		{Path: []string{"enabledPlugins", "fmt@tools"}, Action: ActAdd},
		{Path: []string{"model"}, Action: ActAdd},
	}})
	must(t, err)
	want := "{\n  \"enabledPlugins\": {\n    \"fmt@tools\": true\n  },\n  \"model\": \"opus\"\n}\n"
	if string(out) != want || !rec.Created {
		t.Fatalf("out = %q (created %v)", out, rec.Created)
	}
	must(t, writeAtomic(dst, out))
	_, remove, err := revertMerge(rec)
	must(t, err)
	if !remove {
		t.Fatal("a file the merge created must be removed by undo")
	}
}

func TestHooksKeepBothAppendsAndUndoTakesOnlyThoseOut(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "w", "settings.json"), filepath.Join(dir, "h", "settings.json")
	orig := "{\n  \"hooks\": {\n    \"Stop\": [\n      {\"hooks\": [{\"type\": \"command\", \"command\": \"mine\"}]}\n    ]\n  }\n}\n"
	put(t, dst, orig)
	put(t, src, "{\"hooks\": {\"Stop\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"theirs\"}]}, {\"hooks\": [{\"type\": \"command\", \"command\": \"mine\"}]}]}}")
	out, rec, _, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{{Path: []string{"hooks", "Stop"}, Action: ActAppend}}})
	must(t, err)
	m := decodeAny(t, out)
	if n := len(m["hooks"].(map[string]any)["Stop"].([]any)); n != 2 {
		t.Fatalf("Stop has %d entries:\n%s", n, out)
	}
	must(t, writeAtomic(dst, out))
	back, _, err := revertMerge(rec)
	must(t, err)
	if string(back) != orig {
		t.Fatalf("undo = %q", back)
	}
}

func TestBOMAndEmptyObjectsSurvive(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "w", "settings.json"), filepath.Join(dir, "h", "settings.json")
	orig := "\xEF\xBB\xBF{}"
	put(t, dst, orig)
	put(t, src, `{"model": "opus"}`)
	out, rec, _, err := prepareMerge(JSONMerge{File: dst, Source: src, Ops: []MemberOp{{Path: []string{"model"}, Action: ActAdd}}})
	must(t, err)
	if !bytes.HasPrefix(out, utf8BOM) || decodeAny(t, out)["model"] != "opus" {
		t.Fatalf("out = %q", out)
	}
	must(t, writeAtomic(dst, out))
	back, remove, err := revertMerge(rec)
	must(t, err)
	if remove || string(back) != orig {
		t.Fatalf("undo = %q (remove %v)", back, remove)
	}
}
