package acctable

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// ShortenPath fits a path into w cells by cutting it in the middle: the
// drive (or root) and the last folder are kept, and as much of the start of
// what lies between them as fits, so `C:\Projects\quiz-slayer` in 20 cells is
// `C:\Proj…\quiz-slayer`. ell is the tier's ellipsis ("…" or "...").
//
// A path that cannot be shortened that way — it has no middle, or w is
// smaller than drive + ellipsis + last folder — gives up the drive next
// (`…\quiz-slayer`) and the end of the last folder last. Widths are display
// cells, so wide characters are never split.
func ShortenPath(p string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(p) <= w {
		return p
	}
	head, mid, sep, last := splitPath(p)
	if mid != "" {
		minimal := head + ell + sep + last
		if room := w - ansi.StringWidth(minimal); room >= 0 {
			return head + ansi.Truncate(mid, room, "") + ell + sep + last
		}
	}
	if sep == "" {
		sep = `\`
	}
	if tail := ell + sep + last; head+mid != "" && ansi.StringWidth(tail) <= w {
		return tail
	}
	return ansi.Truncate(last, w, ell)
}

// PathMin is the narrowest [ShortenPath] can make p while still keeping its
// drive and its last folder: the floor below which the table stops
// shortening and wraps instead.
func PathMin(p, ell string) int {
	full := ansi.StringWidth(p)
	head, mid, sep, last := splitPath(p)
	if mid == "" {
		return full
	}
	return min(full, ansi.StringWidth(head+ell+sep+last))
}

// splitPath takes a path apart into its root ("C:\", "/", `\\server\`), the
// folders between the root and the last one, the separator before the last
// one, and the last one. Both separators are understood, so a path reads the
// same whichever way it was typed. A trailing separator is ignored.
func splitPath(p string) (head, mid, sep, last string) {
	p = strings.TrimRight(p, `\/`)
	rest := p
	switch {
	case strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`):
		// A UNC path keeps \\server\ as its root.
		if i := strings.IndexAny(p[2:], `\/`); i >= 0 {
			head, rest = p[:i+3], p[i+3:]
		}
	default:
		if i := strings.IndexAny(p, `\/`); i >= 0 {
			head, rest = p[:i+1], p[i+1:]
		}
	}
	i := strings.LastIndexAny(rest, `\/`)
	if i < 0 {
		return head, "", "", rest
	}
	return head, rest[:i], rest[i : i+1], rest[i+1:]
}

// ShortenEmail fits a quiet account detail into w cells. An email keeps its
// domain and the start of its local part, `(you.with.a.long.name@work.com)`
// becoming `(you.wi…@work.com)`, because the domain is what tells a work
// account from a personal one; an email whose domain no longer fits is
// dropped whole rather than cut to a fragment. Anything else is cut at the
// end. It returns "" when the detail should be dropped.
func ShortenEmail(d string, w int, ell string) string {
	if ansi.StringWidth(d) <= w {
		return d
	}
	if w < detailMin {
		return ""
	}
	if at := strings.LastIndexByte(d, '@'); at > 0 {
		open, local, rest := "", d[:at], d[at:]
		if local[0] == '(' || local[0] == '<' {
			open, local = local[:1], local[1:]
		}
		minimal := open + ell + rest
		room := w - ansi.StringWidth(minimal)
		if room < 0 {
			return ""
		}
		return open + ansi.Truncate(local, room, "") + ell + rest
	}
	return ansi.Truncate(d, w, ell)
}

// detailMin is the narrowest a shortened detail may be before it is dropped.
const detailMin = 6

// splitWhy separates a reason that ends in a path, such as
// "folder rule: C:\Projects", into its words and its path, for a row that did
// not set [Row.WhyPath]. A reason with no path in it comes back whole.
func splitWhy(why string) (words, path string) {
	i := strings.LastIndexByte(why, ' ')
	tail := why[i+1:]
	if !strings.ContainsAny(tail, `\/`) {
		return why, ""
	}
	return strings.TrimRight(why[:max(0, i)], " "), tail
}

// ellipsis is the mark a cut ends with, in the tier's spelling.
func ellipsis(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "..."
	}
	return "…"
}

// fit cuts plain text to at most w cells, marking the cut.
func fit(s string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w <= ansi.StringWidth(ell) {
		return ansi.Truncate(s, w, "")
	}
	return ansi.Truncate(s, w, ell)
}

// pad returns n spaces.
func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// width is ansi.StringWidth, short because the layout code says it a lot.
func width(s string) int { return ansi.StringWidth(s) }

// Glyph resolves an icon key to the glyph the tier has for it, or "" when it
// has none. Keys are the [icons.Set] field names in lower case, plus the home
// section ids.
func Glyph(ic icons.Set, key string) string {
	switch strings.ToLower(key) {
	case "":
		return ""
	case "folder":
		return ic.Folder
	case "file":
		return ic.File
	case "node":
		return ic.Node
	case "docker":
		return ic.Docker
	case "git":
		return ic.Git
	case "windows":
		return ic.Windows
	case "python":
		return ic.Python
	case "rust":
		return ic.Rust
	case "go":
		return ic.Go
	case "trash":
		return ic.Trash
	case "gear":
		return ic.Gear
	case "globe":
		return ic.Globe
	case "key":
		return ic.Key
	case "update":
		return ic.Update
	case "package":
		return ic.Package
	case "clock":
		return ic.Clock
	default:
		return ic.Section(strings.ToLower(key))
	}
}
