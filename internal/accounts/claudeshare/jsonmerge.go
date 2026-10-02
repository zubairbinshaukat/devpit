package claudeshare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// JSON files (settings.json, .claude.json) are changed member by member on
// hujson's tree, which keeps every byte it is not asked to change: the
// person's other keys, their order, their spacing, and the account identity
// in .claude.json stay byte-for-byte as they were. Undo removes or restores
// exactly the members Devpit wrote, and only while they still hold what it
// wrote, so Claude Code rewriting the rest of the file in the meantime never
// blocks an undo.

// Action is what happens to one member.
type Action string

// Actions.
const (
	// ActAdd adds a member the account does not have.
	ActAdd Action = "add"
	// ActAddAs adds the default's member under a new name (Keep both).
	ActAddAs Action = "add-as"
	// ActReplace replaces the account's member; the old value is kept in
	// the backup folder.
	ActReplace Action = "replace"
	// ActAppend adds the default's array elements the account's array
	// lacks (Keep both, hooks).
	ActAppend Action = "append"
)

// MemberOp is one change to one member of a JSON file.
type MemberOp struct {
	// Path is the member in the default's file: container keys, then the
	// name.
	Path   []string
	Action Action
	// NewName is the name it gets in the account's file (ActAddAs).
	NewName string
	// Secret is set when the member may carry secrets.
	Secret bool
}

func (o MemberOp) targetPath() []string {
	if o.Action == ActAddAs && o.NewName != "" {
		p := append([]string(nil), o.Path[:len(o.Path)-1]...)
		return append(p, o.NewName)
	}
	return o.Path
}

// JSONMerge merges chosen members of Source into File.
type JSONMerge struct {
	File   string
	Source string
	Ops    []MemberOp
	// Sidecar is where replaced values are kept, inside the backup folder.
	Sidecar string
}

// jsonDoc is a parsed JSON file that remembers how it was written.
type jsonDoc struct {
	bom     bool
	missing bool
	root    hujson.Value
	obj     *hujson.Object
	nl      string
	unit    string
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func parseDoc(b []byte) (*jsonDoc, error) {
	d := &jsonDoc{}
	if bytes.HasPrefix(b, utf8BOM) {
		d.bom = true
		b = b[len(utf8BOM):]
	}
	d.nl, d.unit = detectStyle(b)
	v, err := hujson.Parse(b)
	if err != nil {
		return nil, err
	}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("it is not a JSON object")
	}
	d.root, d.obj = v, obj
	return d, nil
}

// loadDoc reads path. A missing file is an empty object that remembers it
// was missing.
func loadDoc(path string) (*jsonDoc, error) {
	b, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		d, _ := parseDoc([]byte("{}\n"))
		d.missing = true
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := parseDoc(b)
	if err != nil {
		return nil, fmt.Errorf("%s could not be read as JSON: %w", path, err)
	}
	return d, nil
}

func (d *jsonDoc) bytes() []byte {
	out := d.root.Pack()
	if d.bom {
		out = append(append([]byte(nil), utf8BOM...), out...)
	}
	return out
}

// detectStyle finds the file's line ending and indent unit.
func detectStyle(b []byte) (string, string) {
	nl := "\n"
	if bytes.Contains(b, []byte("\r\n")) {
		nl = "\r\n"
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimLeft(line, " \t")
		if len(t) < len(line) && strings.HasPrefix(t, `"`) {
			return nl, line[:len(line)-len(t)]
		}
	}
	return nl, "  "
}

func memberName(m *hujson.ObjectMember) string {
	if lit, ok := m.Name.Value.(hujson.Literal); ok {
		return lit.String()
	}
	return ""
}

func findMember(obj *hujson.Object, name string) int {
	for i := range obj.Members {
		if memberName(&obj.Members[i]) == name {
			return i
		}
	}
	return -1
}

// object returns the object at path, or nil when a key on the way is
// missing. A key that holds something other than an object is an error.
func (d *jsonDoc) object(path []string) (*hujson.Object, error) {
	cur := d.obj
	for i, k := range path {
		idx := findMember(cur, k)
		if idx < 0 {
			return nil, nil
		}
		next, ok := cur.Members[idx].Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", strings.Join(path[:i+1], "."))
		}
		cur = next
	}
	return cur, nil
}

// member returns the value at path (container keys then name), or nil.
func (d *jsonDoc) member(path []string) *hujson.Value {
	if len(path) == 0 {
		return nil
	}
	obj, err := d.object(path[:len(path)-1])
	if err != nil || obj == nil {
		return nil
	}
	idx := findMember(obj, path[len(path)-1])
	if idx < 0 {
		return nil
	}
	return &obj.Members[idx].Value
}

// members lists the names and values of the object at path.
func (d *jsonDoc) members(path []string) ([]string, map[string]*hujson.Value, error) {
	obj, err := d.object(path)
	if err != nil || obj == nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(obj.Members))
	vals := map[string]*hujson.Value{}
	for i := range obj.Members {
		n := memberName(&obj.Members[i])
		names = append(names, n)
		vals[n] = &obj.Members[i].Value
	}
	return names, vals, nil
}

// canonical is a value's JSON with sorted keys and no spacing or comments:
// two values are the same setting when their canonical forms are equal.
func canonical(v hujson.Value) ([]byte, error) {
	c := v.Clone()
	c.BeforeExtra, c.AfterExtra = nil, nil
	c.Standardize()
	dec := json.NewDecoder(bytes.NewReader(c.Pack()))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	return json.Marshal(x)
}

func valueHash(v hujson.Value) string {
	c, err := canonical(v)
	if err != nil {
		return "unreadable"
	}
	return hashBytes(c)
}

func sameValue(a, b hujson.Value) bool {
	ca, err1 := canonical(a)
	cb, err2 := canonical(b)
	return err1 == nil && err2 == nil && bytes.Equal(ca, cb)
}

// trimmed packs a value without the spacing around it.
func trimmed(v hujson.Value) string {
	c := v.Clone()
	c.BeforeExtra, c.AfterExtra = nil, nil
	return string(c.Pack())
}

// jsonRec is what a merge did, kept in the journal to undo it. It holds
// paths, member names and hashes, never values; replaced values are in the
// sidecar file in the account's backup folder.
type jsonRec struct {
	File              string       `json:"file"`
	Created           bool         `json:"created,omitempty"`
	Sidecar           string       `json:"sidecar,omitempty"`
	CreatedContainers [][]string   `json:"created_containers,omitempty"`
	EmptyBefore       []emptyExtra `json:"empty_before,omitempty"`
	Ops               []jsonOpRec  `json:"ops"`
}

type emptyExtra struct {
	Path  []string `json:"path"`
	Extra string   `json:"extra"`
}

type jsonOpRec struct {
	Path     []string `json:"path"`
	Action   Action   `json:"action"`
	Hash     string   `json:"hash,omitempty"`
	OldHash  string   `json:"old_hash,omitempty"`
	Appended []string `json:"appended,omitempty"`
}

type sideEntry struct {
	Path  []string `json:"path"`
	Value string   `json:"value"`
}

// guardClaudeJSON refuses any change to a .claude.json but its user-scope
// mcpServers entries: the rest of that file is the account's identity.
func guardClaudeJSON(file string, paths ...[]string) error {
	if !strings.EqualFold(filepath.Base(file), ".claude.json") {
		return nil
	}
	for _, p := range paths {
		if len(p) != 2 || p[0] != "mcpServers" {
			return fmt.Errorf("refusing to change %q in %s: Devpit only ever adds MCP server entries to that file", strings.Join(p, "."), file)
		}
	}
	return nil
}

// prepareMerge computes the merged file without writing anything.
func prepareMerge(m JSONMerge) ([]byte, jsonRec, []byte, error) {
	rec := jsonRec{File: m.File}
	for _, op := range m.Ops {
		if err := guardClaudeJSON(m.File, op.Path, op.targetPath()); err != nil {
			return nil, rec, nil, err
		}
		if len(op.Path) == 0 {
			return nil, rec, nil, errors.New("an empty member path")
		}
	}
	dst, err := loadDoc(m.File)
	if err != nil {
		return nil, rec, nil, err
	}
	src, err := loadDoc(m.Source)
	if err != nil {
		return nil, rec, nil, err
	}
	if src.missing {
		return nil, rec, nil, fmt.Errorf("%s is gone", m.Source)
	}
	rec.Created = dst.missing
	var side []sideEntry
	for _, op := range m.Ops {
		sv := src.member(op.Path)
		if sv == nil {
			return nil, rec, nil, fmt.Errorf("%s no longer has %q", m.Source, strings.Join(op.Path, "."))
		}
		tp := op.targetPath()
		parentPath, name := tp[:len(tp)-1], tp[len(tp)-1]
		switch op.Action {
		case ActAdd, ActAddAs:
			parent, err := dst.ensure(parentPath, &rec)
			if err != nil {
				return nil, rec, nil, err
			}
			if findMember(parent, name) >= 0 {
				return nil, rec, nil, fmt.Errorf("%s already has %q; look at the preview again", m.File, strings.Join(tp, "."))
			}
			val := sv.Clone()
			dst.insert(parent, parentPath, name, val, &rec)
			rec.Ops = append(rec.Ops, jsonOpRec{Path: tp, Action: op.Action, Hash: valueHash(val)})
		case ActReplace:
			cur := dst.member(tp)
			if cur == nil {
				return nil, rec, nil, fmt.Errorf("%s no longer has %q; look at the preview again", m.File, strings.Join(tp, "."))
			}
			side = append(side, sideEntry{Path: tp, Value: trimmed(*cur)})
			oldHash := valueHash(*cur)
			nv := sv.Clone()
			nv.BeforeExtra, nv.AfterExtra = cur.BeforeExtra, cur.AfterExtra
			*cur = nv
			rec.Ops = append(rec.Ops, jsonOpRec{Path: tp, Action: ActReplace, Hash: valueHash(nv), OldHash: oldHash})
		case ActAppend:
			cur := dst.member(tp)
			if cur == nil {
				return nil, rec, nil, fmt.Errorf("%s no longer has %q", m.File, strings.Join(tp, "."))
			}
			arr, ok1 := cur.Value.(*hujson.Array)
			sarr, ok2 := sv.Value.(*hujson.Array)
			if !ok1 || !ok2 {
				return nil, rec, nil, fmt.Errorf("%q is not a list in both files", strings.Join(tp, "."))
			}
			var added []string
			for _, el := range sarr.Elements {
				if containsValue(arr.Elements, el) {
					continue
				}
				ne := el.Clone()
				ne.AfterExtra = nil
				if n := len(arr.Elements); n > 0 {
					ne.BeforeExtra = hujson.Extra(lineIndent(arr.Elements[n-1].BeforeExtra, dst.nl+strings.Repeat(dst.unit, len(tp)+1)))
				} else {
					ne.BeforeExtra = hujson.Extra(dst.nl + strings.Repeat(dst.unit, len(tp)+1))
				}
				arr.Elements = append(arr.Elements, ne)
				added = append(added, valueHash(ne))
			}
			rec.Ops = append(rec.Ops, jsonOpRec{Path: tp, Action: ActAppend, Appended: added})
		default:
			return nil, rec, nil, fmt.Errorf("unknown change %q", op.Action)
		}
	}
	var sideBytes []byte
	if len(side) > 0 {
		if m.Sidecar == "" {
			return nil, rec, nil, errors.New("replacing a value needs a backup folder")
		}
		rec.Sidecar = m.Sidecar
		sideBytes, _ = json.MarshalIndent(side, "", "  ")
	}
	return dst.bytes(), rec, sideBytes, nil
}

func containsValue(list []hujson.Value, v hujson.Value) bool {
	for _, x := range list {
		if sameValue(x, v) {
			return true
		}
	}
	return false
}

// lineIndent is the newline and indent at the end of an Extra, or the
// Extra itself when it holds no newline (a one-line object).
func lineIndent(extra hujson.Extra, fallback string) string {
	s := string(extra)
	i := strings.LastIndexByte(s, '\n')
	if i < 0 {
		if strings.TrimSpace(s) == "" {
			return s
		}
		return fallback
	}
	ws := s[i+1:]
	if strings.TrimLeft(ws, " \t") != "" {
		return fallback
	}
	nl := "\n"
	if i > 0 && s[i-1] == '\r' {
		nl = "\r\n"
	}
	return nl + ws
}

// ensure returns the object at path, creating empty objects on the way.
func (d *jsonDoc) ensure(path []string, rec *jsonRec) (*hujson.Object, error) {
	cur := d.obj
	for i, k := range path {
		idx := findMember(cur, k)
		if idx < 0 {
			child := &hujson.Object{}
			d.insert(cur, path[:i], k, hujson.Value{Value: child}, rec)
			rec.CreatedContainers = append(rec.CreatedContainers, append([]string(nil), path[:i+1]...))
			cur = child
			continue
		}
		next, ok := cur.Members[idx].Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", strings.Join(path[:i+1], "."))
		}
		cur = next
	}
	return cur, nil
}

// insert appends name: val to parent (at parentPath), spaced like its
// siblings.
func (d *jsonDoc) insert(parent *hujson.Object, parentPath []string, name string, val hujson.Value, rec *jsonRec) {
	depth := len(parentPath) + 1
	before := d.nl + strings.Repeat(d.unit, depth)
	if n := len(parent.Members); n > 0 {
		before = lineIndent(parent.Members[n-1].Name.BeforeExtra, before)
	} else {
		if !isCreated(rec, parentPath) {
			rec.EmptyBefore = append(rec.EmptyBefore, emptyExtra{Path: append([]string(nil), parentPath...), Extra: string(parent.AfterExtra)})
		}
		parent.AfterExtra = hujson.Extra(d.nl + strings.Repeat(d.unit, depth-1))
	}
	val.BeforeExtra = hujson.Extra(" ")
	val.AfterExtra = nil
	parent.Members = append(parent.Members, hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: hujson.Extra(before), Value: hujson.String(name)},
		Value: val,
	})
}

func isCreated(rec *jsonRec, path []string) bool {
	for _, c := range rec.CreatedContainers {
		if equalPath(c, path) {
			return true
		}
	}
	return false
}

func equalPath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jsonState says whether the members a merge wrote are still there.
func jsonState(rec jsonRec) (int, error) {
	d, err := loadDoc(rec.File)
	if err != nil {
		return stChanged, err
	}
	after, changed := 0, false
	for _, op := range rec.Ops {
		cur := d.member(op.Path)
		switch op.Action {
		case ActAdd, ActAddAs:
			switch {
			case cur == nil:
			case op.Hash == "" || valueHash(*cur) == op.Hash:
				after++
			default:
				changed = true
			}
		case ActReplace:
			switch {
			case cur == nil:
				changed = true
			case valueHash(*cur) == op.Hash:
				after++
			case valueHash(*cur) != op.OldHash:
				changed = true
			}
		case ActAppend:
			if cur == nil {
				continue
			}
			arr, ok := cur.Value.(*hujson.Array)
			if !ok {
				changed = true
				continue
			}
			if tailMatches(arr, op.Appended) && len(op.Appended) > 0 {
				after++
			}
		}
	}
	switch {
	case changed:
		return stChanged, nil
	case after == 0:
		return stBefore, nil
	}
	return stAfter, nil
}

func tailMatches(arr *hujson.Array, hashes []string) bool {
	n := len(arr.Elements)
	if len(hashes) > n {
		return false
	}
	for i, h := range hashes {
		if valueHash(arr.Elements[n-len(hashes)+i]) != h {
			return false
		}
	}
	return true
}

// revertMerge removes or restores exactly the members a merge wrote. It
// returns the file's new bytes, or remove=true when the merge created the
// file and nothing else is in it now.
func revertMerge(rec jsonRec) ([]byte, bool, error) {
	d, err := loadDoc(rec.File)
	if err != nil {
		return nil, false, err
	}
	if d.missing {
		return nil, false, nil
	}
	side := map[string]string{}
	if rec.Sidecar != "" {
		if b, err := readFile(rec.Sidecar); err == nil {
			var list []sideEntry
			if err := json.Unmarshal(b, &list); err != nil {
				return nil, false, fmt.Errorf("the kept values in %s could not be read: %w", rec.Sidecar, err)
			}
			for _, e := range list {
				side[strings.Join(e.Path, "\x00")] = e.Value
			}
		}
	}
	for i := len(rec.Ops) - 1; i >= 0; i-- {
		op := rec.Ops[i]
		parentPath, name := op.Path[:len(op.Path)-1], op.Path[len(op.Path)-1]
		parent, err := d.object(parentPath)
		if err != nil {
			return nil, false, err
		}
		if parent == nil {
			continue
		}
		idx := findMember(parent, name)
		switch op.Action {
		case ActAdd, ActAddAs:
			if idx < 0 {
				continue
			}
			if op.Hash != "" && valueHash(parent.Members[idx].Value) != op.Hash {
				return nil, false, fmt.Errorf("%q in %s was changed since Devpit added it", strings.Join(op.Path, "."), rec.File)
			}
			removeMemberAt(parent, idx)
			d.restoreEmpty(parent, parentPath, rec)
		case ActReplace:
			if idx < 0 {
				return nil, false, fmt.Errorf("%q in %s is gone", strings.Join(op.Path, "."), rec.File)
			}
			cur := &parent.Members[idx].Value
			if valueHash(*cur) == op.OldHash {
				continue
			}
			old, ok := side[strings.Join(op.Path, "\x00")]
			if !ok {
				return nil, false, fmt.Errorf("the old value of %q is missing from %s", strings.Join(op.Path, "."), rec.Sidecar)
			}
			ov, err := hujson.Parse([]byte(old))
			if err != nil {
				return nil, false, err
			}
			ov.BeforeExtra, ov.AfterExtra = cur.BeforeExtra, cur.AfterExtra
			*cur = ov
		case ActAppend:
			if idx < 0 {
				continue
			}
			arr, ok := parent.Members[idx].Value.Value.(*hujson.Array)
			if !ok || !tailMatches(arr, op.Appended) {
				continue
			}
			arr.Elements = arr.Elements[:len(arr.Elements)-len(op.Appended)]
		}
	}
	for i := len(rec.CreatedContainers) - 1; i >= 0; i-- {
		c := rec.CreatedContainers[i]
		parentPath := c[:len(c)-1]
		parent, err := d.object(parentPath)
		if err != nil || parent == nil {
			continue
		}
		idx := findMember(parent, c[len(c)-1])
		if idx < 0 {
			continue
		}
		if obj, ok := parent.Members[idx].Value.Value.(*hujson.Object); ok && len(obj.Members) == 0 {
			removeMemberAt(parent, idx)
			d.restoreEmpty(parent, parentPath, rec)
		}
	}
	if rec.Created && len(d.obj.Members) == 0 {
		return nil, true, nil
	}
	return d.bytes(), false, nil
}

func removeMemberAt(obj *hujson.Object, idx int) {
	obj.Members = append(obj.Members[:idx], obj.Members[idx+1:]...)
}

func (d *jsonDoc) restoreEmpty(parent *hujson.Object, path []string, rec jsonRec) {
	if len(parent.Members) != 0 {
		return
	}
	for _, e := range rec.EmptyBefore {
		if equalPath(e.Path, path) {
			parent.AfterExtra = hujson.Extra(e.Extra)
			if e.Extra == "" {
				parent.AfterExtra = nil
			}
			return
		}
	}
}

// Effect states as plain ints, mapped to the engine's in effects.go.
const (
	stBefore = iota
	stAfter
	stChanged
)
