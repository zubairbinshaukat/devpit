package restable

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// yard is the repository the monorepo fixture lives in.
const yard = `D:\YardTerminal`

// junk builds one item under a project, optionally inside a repository.
func junk(repo, project, name string, size uint64, tier scan.Tier) scan.Item {
	return scan.Item{
		Path: project + `\` + name, Name: name, Project: project, Repo: repo,
		Size: size, Tier: tier, Kind: scan.KindProjectJunk,
		Rule: name, RestoreHint: "rebuild it",
		LastUsed: testNow.Add(-40 * 24 * time.Hour),
	}
}

// monorepo is the screenshot that started this: a repository with three
// projects in it, two unrelated folders both called admin, and an ordinary
// single-project repository.
func monorepo() []scan.Item {
	return []scan.Item{
		junk(yard, yard+`\apps\admin`, "node_modules", 10_900_000, scan.TierSafe),
		junk(yard, yard+`\apps\admin`, "dist", 1_400_000, scan.TierReview),
		junk(yard, yard+`\apps\web`, "node_modules", 15_700_000, scan.TierSafe),
		junk(yard, yard, "node_modules", 90_000_000, scan.TierSafe),
		junk("", `D:\client\apps\admin`, "node_modules", 5_000_000, scan.TierSafe),
		junk("", `D:\stuff\packages\admin`, "node_modules", 4_000_000, scan.TierSafe),
		junk(`D:\solo`, `D:\solo`, "node_modules", 3_000_000, scan.TierSafe),
	}
}

// treeTable is a finished, sized table over the monorepo fixture.
func treeTable() Model {
	return New().
		SetNow(func() time.Time { return testNow }).
		SetSize(100, 20).
		SetItems(monorepo()).
		SetStreaming(false)
}

// shape renders the row list as one line per row: headings as "#label" with
// "  " per level of nesting, items as their name. It is the tree without the
// columns, which is what these tests are about.
func shape(m Model) []string {
	out := make([]string, 0, len(m.rows))
	for _, r := range m.rows {
		depth := indent(r) / nestStep
		if r.kind == rowGroup {
			out = append(out, strings.Repeat("  ", depth)+"#"+m.node(r).label.String())
			continue
		}
		out = append(out, strings.Repeat("  ", depth)+m.items[r.item].Name)
	}
	return out
}

// plainView renders the table with colour stripped, split into lines.
func plainView(m Model) []string {
	return strings.Split(ansi.Strip(m.View(testContext(100, 30))), "\n")
}

// A repository with several projects gets a heading of its own, with its
// projects under it by their path inside it; the two unrelated admins are
// told apart by their parent folder; and a repository that is one project at
// its root is drawn exactly like a plain project.
func TestMonorepoNestsAndLabelsDisambiguate(t *testing.T) {
	got := shape(treeTable())
	want := []string{
		"#YardTerminal",
		"  #(root)",
		"    node_modules",
		"  #apps/web",
		"    node_modules",
		"  #apps/admin",
		"    node_modules",
		"    dist",
		"#apps/admin",
		"node_modules",
		"#packages/admin",
		"node_modules",
		"#solo",
		"node_modules",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the tree is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Without a repository anywhere the table is the flat list it always was:
// every heading at the top level, every item straight under it.
func TestNoRepositoryKeepsTheFlatLayout(t *testing.T) {
	for _, r := range table().rows {
		if r.sub != -1 {
			t.Fatalf("a scan with no repository produced a nested row: %+v", r)
		}
	}
}

// The heading counts: a repository reports its projects, a project its
// items, and both carry the summed size.
func TestHeadingsAggregate(t *testing.T) {
	lines := plainView(treeTable())
	repo := lineWith(t, lines, "YardTerminal")
	if !strings.Contains(repo, "3 projects") {
		t.Errorf("the repository heading does not count its projects: %q", repo)
	}
	if got := treeTable().groups[0].size; got != 118_000_000 {
		t.Errorf("the repository heading sums to %d bytes, want 118000000", got)
	}
	if !strings.Contains(repo, "113 MB") {
		t.Errorf("the repository heading does not show its total: %q", repo)
	}
	admin := lineWith(t, lines, "apps/admin")
	if !strings.Contains(admin, "2 items") {
		t.Errorf("the project heading does not count its items: %q", admin)
	}
}

// lineWith returns the first line holding s.
func lineWith(t *testing.T, lines []string, s string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, s) {
			return l
		}
	}
	t.Fatalf("no line holds %q:\n%s", s, strings.Join(lines, "\n"))
	return ""
}

// Ticking a repository heading ticks every item in every project under it;
// a partly ticked heading fills rather than clears.
func TestRepositoryHeadingTicksEverythingUnderIt(t *testing.T) {
	m := treeTable()
	g := &m.groups[0]
	if got, total := m.ticked(g), len(g.items); got == 0 || got == total {
		t.Fatalf("the fixture should start partly ticked, got %d of %d", got, total)
	}
	m, _ = m.Update(press(" ")) // the cursor starts on the repository heading
	if got := m.ticked(&m.groups[0]); got != len(m.groups[0].items) {
		t.Fatalf("space on a partly ticked repository ticked %d of %d", got, len(m.groups[0].items))
	}
	m, _ = m.Update(press(" "))
	if got := m.ticked(&m.groups[0]); got != 0 {
		t.Fatalf("space on a fully ticked repository left %d ticked", got)
	}
	for _, it := range m.Selected() {
		if it.Repo == yard {
			t.Fatalf("%s is still selected", it.Path)
		}
	}
}

// The partial glyph appears on a heading with some, not all, items ticked.
func TestPartialHeadingShowsThePartialBox(t *testing.T) {
	lines := plainView(treeTable())
	if repo := lineWith(t, lines, "YardTerminal"); !strings.Contains(repo, "[-]") {
		t.Errorf("a partly ticked repository heading has no partial box: %q", repo)
	}
	if admin := lineWith(t, lines, "apps/admin "); !strings.Contains(admin, "[-]") {
		t.Errorf("a partly ticked project heading has no partial box: %q", admin)
	}
	if web := lineWith(t, lines, "apps/web"); !strings.Contains(web, "[x]") {
		t.Errorf("a fully ticked project heading is not ticked: %q", web)
	}
}

// Folding works at both levels: ← on a project folds it, ← again folds its
// repository, and → brings each back.
func TestFoldingWorksAtBothLevels(t *testing.T) {
	m := treeTable()
	full := len(m.rows)

	m, _ = m.Update(special(tea.KeyDown)) // (root) heading
	m, _ = m.Update(special(tea.KeyDown)) // its node_modules
	m, _ = m.Update(special(tea.KeyLeft))
	if got := len(m.rows); got != full-1 {
		t.Fatalf("← on an item folded to %d rows, want %d", got, full-1)
	}
	if r := m.rows[m.cursor]; r.kind != rowGroup || r.sub != 0 {
		t.Fatalf("← on an item left the cursor on %+v, want its project heading", r)
	}

	m, _ = m.Update(special(tea.KeyLeft))
	if r := m.rows[m.cursor]; r.kind != rowGroup || r.sub != -1 {
		t.Fatalf("← on a folded project left the cursor on %+v, want the repository heading", r)
	}
	if got := shape(m)[1]; got != "#apps/admin" {
		t.Fatalf("after folding the repository the next row is %q, want the next top-level heading", got)
	}

	m, _ = m.Update(special(tea.KeyRight))
	if got := len(m.rows); got != full-1 {
		t.Fatalf("→ on the repository gave %d rows, want %d (the project stays folded)", got, full-1)
	}
	m, _ = m.Update(special(tea.KeyDown))
	m, _ = m.Update(special(tea.KeyRight))
	if got := len(m.rows); got != full {
		t.Fatalf("→ on the project gave %d rows, want %d", got, full)
	}
}

// A filter keeps a heading as long as something under it matches, and keeps
// the labels it had: typing never re-labels what is left.
func TestFilterKeepsHeadingsOfMatches(t *testing.T) {
	m := treeTable()
	m, _ = m.Update(press("/"))
	for _, r := range "dist" {
		m, _ = m.Update(press(string(r)))
	}
	m, _ = m.Update(special(tea.KeyEnter))

	got := strings.Join(shape(m), "\n")
	want := strings.Join([]string{"#YardTerminal", "  #apps/admin", "    dist"}, "\n")
	if got != want {
		t.Errorf("the filtered tree is\n%s\nwant\n%s", got, want)
	}
}

// Sorting applies at every level: by name, the projects inside the
// repository and the top-level headings are each alphabetical.
func TestSortAppliesAtEveryLevel(t *testing.T) {
	m := treeTable()
	m, _ = m.Update(press("s")) // name
	got := shape(m)
	var top, kids []string
	for _, s := range got {
		switch {
		case strings.HasPrefix(s, "#"):
			top = append(top, s)
		case strings.HasPrefix(s, "  #"):
			kids = append(kids, s)
		}
	}
	if strings.Join(top, ",") != "#apps/admin,#packages/admin,#solo,#YardTerminal" {
		t.Errorf("top-level headings by name: %v", top)
	}
	if strings.Join(kids, ",") != "  #(root),  #apps/admin,  #apps/web" {
		t.Errorf("projects by name: %v", kids)
	}
}

// Moving the cursor and paging cross both levels, and the window keeps the
// cursor on screen all the way down and back.
func TestCursorWalksTheWholeTree(t *testing.T) {
	m := treeTable().SetSize(100, 6)
	for range len(m.rows) + 3 {
		m, _ = m.Update(special(tea.KeyDown))
		if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() {
			t.Fatalf("cursor %d fell outside the window %d+%d", m.cursor, m.top, m.bodyHeight())
		}
	}
	if m.cursor != len(m.rows)-1 {
		t.Fatalf("↓ stopped at %d, want the last row %d", m.cursor, len(m.rows)-1)
	}
	m, _ = m.Update(special(tea.KeyPgUp))
	m, _ = m.Update(special(tea.KeyPgUp))
	m, _ = m.Update(special(tea.KeyPgUp))
	if m.cursor != 0 || m.top != 0 {
		t.Fatalf("page up three times left the cursor at %d and the top at %d", m.cursor, m.top)
	}
}

// Preselection means the same thing nested as flat: only the plain Safe
// finds are ticked, and nesting never ticks a heading's other items.
func TestNestingDoesNotChangePreselection(t *testing.T) {
	m := treeTable()
	for _, it := range m.Selected() {
		if it.Tier != scan.TierSafe {
			t.Errorf("%s (%s) is pre-ticked", it.Path, it.Tier)
		}
	}
	if n, _ := m.SelectedCount(); n != 6 {
		t.Errorf("%d items pre-ticked, want the 6 Safe ones", n)
	}
}

// The labels use the shortest suffix nobody else has, falling back to the
// volume when two paths differ only there, and to the whole path when one
// path is the tail of another.
func TestDisambiguate(t *testing.T) {
	got := disambiguate(map[string]string{
		"a": `D:\work\apps\admin`,
		"b": `D:\work\packages\admin`,
		"c": `D:\x\web`,
		"d": `E:\x\web`,
		"e": `D:\api`,
		"f": `D:\api\api`,
		"g": `D:\`,
	})
	want := map[string]string{
		"a": `apps/admin`,
		"b": `packages/admin`,
		"c": `D:\…\web`,
		"d": `E:\…\web`,
		"e": `D:\api`,
		"f": `api/api`,
		"g": `D:\`,
	}
	for k, w := range want {
		if got[k].String() != w {
			t.Errorf("%s: label %q, want %q", k, got[k].String(), w)
		}
	}
	if l := got["a"]; l.dir != "apps/" || l.base != "admin" {
		t.Errorf("the label splits as %q + %q, want the parent muted and the name apart", l.dir, l.base)
	}
}

// A label too long for its column loses its context from the left, never
// its name.
func TestFitLabelKeepsTheName(t *testing.T) {
	l := fitLabel(label{dir: "packages/very/deep/", base: "admin"}, 12)
	if got := l.String(); got != "…deep/admin" && ansi.StringWidth(got) != 12 {
		t.Errorf("fitted label %q is %d cells", got, ansi.StringWidth(got))
	}
	if !strings.HasSuffix(l.base, "admin") || ansi.StringWidth(l.String()) != 12 {
		t.Errorf("fitted label %q lost its name or its width", l.String())
	}
}
