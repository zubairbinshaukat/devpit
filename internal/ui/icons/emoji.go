package icons

// The emoji allow-list.
//
// Emoji appear only in free text: the header, a summary card, the first-run
// screen and empty states. They never appear in tables, menus or the key-hint
// bar, because every one of them is two cells wide and would break alignment.
//
// Each entry must be a single codepoint with the Emoji_Presentation property,
// so no terminal has to guess whether to render it narrow. A test asserts each
// is exactly two cells.
const (
	// EmojiFlag is the chequered flag: finished, back on track.
	EmojiFlag = "\U0001F3C1"
	// EmojiRocket marks speed and launch.
	EmojiRocket = "\U0001F680"
	// EmojiBroom marks cleaning.
	EmojiBroom = "\U0001F9F9"
	// EmojiWrench marks tools and settings.
	EmojiWrench = "\U0001F527"
	// EmojiSparkles marks a clean result.
	EmojiSparkles = "✨"
)

// AllowedEmoji is the complete, closed set. Nothing outside it may be printed.
var AllowedEmoji = []string{
	EmojiFlag,
	EmojiRocket,
	EmojiBroom,
	EmojiWrench,
	EmojiSparkles,
}

// Emoji returns e when emoji are enabled and the empty string when they are
// not, so callers can write fmt.Sprintf("%s Devpit", icons.Emoji(on, flag))
// without branching.
func Emoji(enabled bool, e string) string {
	if !enabled {
		return ""
	}
	return e
}

// IsAllowedEmoji reports whether e is in the allow-list.
func IsAllowedEmoji(e string) bool {
	for _, a := range AllowedEmoji {
		if a == e {
			return true
		}
	}
	return false
}
