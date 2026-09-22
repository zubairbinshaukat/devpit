# Icons

## Why there are tiers at all

Windows Terminal ships Cascadia Mono, which has no Nerd Font glyphs, Microsoft
declined to bundle one, and no program can ask a terminal which fonts it has.
So an app that draws Nerd Font icons by default shows a column of boxes on a
stock Windows machine. Every good TUI — lazygit, k9s, yazi, superfile — makes
them opt-in. Devpit does too.

| Tier | When | What it looks like |
| --- | --- | --- |
| `nerd` | opt-in only | folder, file-type, node, docker and trash glyphs |
| `unicode` | the default on a capable terminal | geometric shapes and box drawing |
| `ascii` | dumb terminals, bare conhost, `--ascii` | nothing above U+007F except the three risk shapes |

## The table

| Use | nerd | unicode | ascii |
| --- | --- | --- | --- |
| folder / open | `U+F07B` / `U+F07C` | `▸` / `▾` | `>` / `v` |
| file | extension map, fallback `U+F15B` | `·` | `-` |
| tick / fail / warn | `U+F00C` / `U+F00D` / `U+F071` | `✓` / `✗` / `!` | `+` / `X` / `!` |
| ticked / not | `U+F14A` / `U+F096` | `[x]` / `[ ]` | `[x]` / `[ ]` |
| Safe / Review / Careful | `●` / `◆` / `▲` in every tier | | |
| node, docker, git, windows, python, rust, go | `U+E718 U+F308 U+E702 U+E70F U+E73C U+E7A8 U+E724` | none | none |
| trash, gear, globe, key, update, package, clock | `U+F1F8 U+F013 U+F0AC U+F084 U+F021 U+F487 U+F017` | none | none |
| bar full / empty | `█` / `░` | `█` / `░` | `#` / `-` |
| cursor | `U+F054` | `▸` | `>` |

Two deviations from the implementation plan, both deliberate:

- The plan writes the ascii tick as `OK`. That is two cells, and a two-cell
  tick breaks every column it sits in, so the ascii tier uses `+`.
- The risk shapes stay geometric in the ascii tier, exactly as the plan says,
  even though they are not ASCII. Risk has to read identically everywhere, and
  all three are one cell in every terminal that can draw them at all.

## The two invariants

**Every non-empty glyph is one terminal cell.** Columns only line up if this
holds. The single exception is the tick box in the unicode and ascii tiers,
which is the three-cell `[x]` / `[ ]`. `TestEveryGlyphIsOneCell` and
`TestTickBoxWidths` pin both, using `ansi.StringWidth`.

**Nerd glyphs are BMP private-use only**, from nf-fa, nf-dev, nf-seti and
nf-oct. Nothing from nf-md, which lives in a supplementary plane that older
terminals render as two cells or not at all. `TestNerdGlyphsAreBMPPrivateUse`
pins it.

A glyph that a tier does not have is the empty string, not a space. Callers draw
nothing and take no columns; the menu component already handles it.

## How a tier is chosen

`config.icons` is `auto` (the default), `nerd`, `unicode` or `ascii`. An
explicit value always wins. `auto` resolves in this order:

1. **ascii** when `--ascii` was passed, `TERM=dumb`, or `NO_COLOR` is set on a
   terminal that is not Windows Terminal;
2. **nerd** when Devpit installed the icon font (`font_installed`), or the
   user answered Yes to the first-run glyph probe;
3. **unicode** when `WT_SESSION` or `TERM` says there is a real terminal;
4. **ascii** otherwise, which is the safe answer for bare conhost.

The first-run screen prints a probe line — `✓ ● ▸` plus two Nerd Font glyphs —
and asks whether all five render. No is the default, so a terminal that cannot
draw them never claims it can.

## Emoji

A closed allow-list of five: 🏁 🚀 🧹 🔧 ✨. Each is a single codepoint with
the Emoji_Presentation property, so no terminal has to guess how wide it is,
and each is two cells. `TestEmojiAreTwoCells` pins that.

They appear only in free text: the header, a summary card, the first-run screen
and empty states. **Never** in a table, a menu row or the key-hint bar, because
two cells in an aligned column is a broken column. One setting turns them off.

## Adding a glyph

1. Add the field to `icons.Set` with a doc comment saying what it is for.
2. Fill it in **all three** constructors in `internal/ui/icons/icons.go`. Use
   the empty string where a tier genuinely has no equivalent.
3. Add it to `Set.Glyphs()`, which is what the width test walks.
4. For a nerd glyph, pick a BMP private-use codepoint from nf-fa, nf-dev,
   nf-seti or nf-oct, and leave the Nerd Fonts class name in a comment beside
   it, the way the existing entries do.
5. Run `go test ./internal/ui/icons`. If the width test fails, the glyph is
   wrong, not the test.
6. Regenerate any golden frame the glyph appears in:
   `go test ./internal/app -run Golden -update`, then read the diff.

## Adding a file-extension icon

`internal/ui/icons/extensions.go` maps a lower-cased **file name** or
**extension with its dot** to a glyph. Full names win over extensions, matching
is case-insensitive, and the fallback is the generic file glyph.

Keep the table small. It exists to make a results table scannable, not to cover
every language that has ever shipped. The table is Devpit's own; its idea and
shape are inspired by lazygit's `file_icons.go`, attributed in `NOTICE`.

## The icon font

Devpit bundles no font. Milestone 4 adds an installer that downloads
Symbols Nerd Font Mono from a pinned Nerd Fonts release, verifies its SHA256,
installs it for the current user without administrator rights, and appends it
as a **fallback** to Windows Terminal's font list. The user's own font is never
replaced, the original `settings.json` is backed up first, and
`devpit font remove` reverses all of it.
