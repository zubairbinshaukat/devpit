// Package about holds the facts about Devpit that are printed in more than
// one place: who made it, where it lives and what licence it carries. The
// installer, the home screen, the settings screen and the CLI all read from
// here, so the byline can never drift between them.
package about

// Identity strings. They are constants so nothing is computed at startup.
const (
	// Name is the product name as it is written in prose.
	Name = "Devpit"
	// Tagline is the one-line description under the wordmark.
	Tagline = "A pit stop for your dev machine"
	// Author is the maker's full name.
	Author = "Zubair Bin Shaukat"
	// Handle is the short name the author goes by online.
	Handle = "zubyr"
	// Byline is the credit line drawn under the wordmark.
	Byline = "by " + Author
	// Portfolio is the author's site.
	Portfolio = "https://zubyr.dev"
	// Website is the product site.
	Website = "https://devpit.zubyr.dev"
	// Repo is the source repository.
	Repo = "https://github.com/zubairbinshaukat/devpit"
	// Issues is where to report a problem.
	Issues = Repo + "/issues"
	// License is the licence Devpit ships under.
	License = "MIT"
)
