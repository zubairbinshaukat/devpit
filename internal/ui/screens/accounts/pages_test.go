package accounts

import (
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts/demo"
)

// The Git page says what is true now in plain words, groups its choices,
// and says what Enter does next.
func TestGitPageIsStatusThenChoices(t *testing.T) {
	h := toolPage(t, demo.New(), 1)
	if _, ok := h.top().(gitScreen); !ok {
		t.Fatalf("row 1 opened %T", h.top())
	}
	h.mustSee("Right now", "Commits as", "from your usual Git settings", "Pushes as",
		"Commits", "Pushes", "Manage", "Commit as someone else here", "Push with an SSH key",
		"Rename or remove a name and email", "You pick one, then see a preview")
	for _, jargon := range []string{"identity", "helper", "…"} {
		if strings.Contains(h.view(), jargon) {
			t.Errorf("the Git page says %q:\n%s", jargon, h.view())
		}
	}
	// The headings are never stopped on: down from the last commit choice
	// lands on the first push choice.
	h.keys("down", "down", "down")
	h.mustSee("Push as another GitHub account here", "Pick which GitHub account your pushes")
	// v checks who is signed in.
	h.keys("v")
	if _, ok := h.top().(verifyScreen); !ok {
		t.Errorf("v opened %T, want the check", h.top())
	}
}

// A tool page leads with what is true now, explains "not checked yet", and
// shows a choice the tool cannot make with the reason, not hidden.
func TestToolPageExplainsItself(t *testing.T) {
	h := toolPage(t, demo.New(), 0)
	h.mustSee("Right now", "Signed in as", "Why this one", "chosen for", "Other folders", "your usual Claude Code sign-in",
		"Change the account", "Sign in", "Claude Code setup", "Manage")

	// Cloudflare's account is not checked yet: the page says what that means.
	h = toolPage(t, demo.New(), 6)
	h.mustSee("your usual Cloudflare sign-in (not checked yet)", "Not checked yet: Devpit has not asked Cloudflare who is signed in. Press v to check.")

	// Convex is shown, not switched: the one choice is there, quiet, with why.
	h = toolPage(t, demo.New(), 7)
	h.mustSee("not available", "Not available now:")
}
