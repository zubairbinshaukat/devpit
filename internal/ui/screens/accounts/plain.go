package accounts

import (
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// The engine words things for the command line and for people who know
// Devpit ("default (not checked yet)", "folder rule: C:\Work", "Git's own
// sign-in helper"). The screens say the same facts in words someone who has
// never opened Accounts understands. These helpers are the one place that
// translation happens, so every screen says it the same way.

// notChecked is the engine's mark for an account Devpit has not asked about.
const notChecked = "(not checked yet)"

// accountPlain names an account for a person: the default account is "your
// usual <tool> sign-in", any other keeps its name and who it is.
func accountPlain(display string, t accounts.Tool) string {
	if display == accounts.DefaultName || strings.HasPrefix(display, accounts.DefaultName+" (") {
		rest := strings.TrimPrefix(display, accounts.DefaultName)
		return "your usual " + t.DisplayName() + " sign-in" + rest
	}
	return display
}

// notCheckedNote says what "not checked yet" means, and how to check.
func notCheckedNote(t accounts.Tool) string {
	return "Not checked yet: Devpit has not asked " + t.DisplayName() + " who is signed in. Press v to check."
}

// isNotChecked reports whether a display carries the not-checked mark.
func isNotChecked(display string) bool { return strings.Contains(display, notChecked) }

// wherePlain says why an account is the one used here: a choice made for a
// folder (and the folders inside it), or the one used everywhere.
func wherePlain(r accounts.Resolution) string {
	if r.Reason != accounts.ReasonFolderRule {
		return "the one used in every folder without a choice of its own"
	}
	s := "chosen for " + r.RuleFolder + " and the folders inside it"
	if r.Via == accounts.ViaResolved && r.ResolvedFolder != "" {
		s += " (reached through " + r.ResolvedFolder + ")"
	}
	return s
}

// fromPlain says which Git setting decided the name and email.
func fromPlain(f adapters.GitFrom) string {
	switch f {
	case adapters.GitFromRule:
		return "a choice you made in Devpit for this folder"
	case adapters.GitFromEverywhere:
		return "a choice you made in Devpit for every folder"
	case adapters.GitFromDefaultRule:
		return "a choice you made in Devpit for this folder: your usual name and email"
	case adapters.GitFromRepo:
		return "this repository's own Git settings"
	case adapters.GitFromGlobal:
		return "your usual Git settings (.gitconfig in your user folder)"
	case adapters.GitFromAfterDevpit:
		return "a line in your .gitconfig after Devpit's part, which wins"
	case adapters.GitFromSystem:
		return "Git's settings for every user of this PC"
	case adapters.GitFromCommand:
		return "the command line or an environment variable"
	case adapters.GitFromOther:
		return "another Git settings file"
	case adapters.GitFromNowhere:
		return "nowhere: Git has no name or email set"
	}
	return string(f)
}

// viaPlain says what hands Git the GitHub sign-in for a push.
func viaPlain(v adapters.GitPushVia) string {
	switch v {
	case adapters.PushViaDevpit:
		return "Devpit gives Git the sign-in chosen for this folder"
	case adapters.PushViaHelper:
		return "Git uses the GitHub sign-in it saved itself"
	case adapters.PushViaRepoHelper:
		return "this repository's own settings pick the sign-in"
	case adapters.PushViaSSHKey:
		return "the SSH key decides"
	case adapters.PushViaNotGitHub:
		return "this repository is not on GitHub"
	case adapters.PushViaNoRemote:
		return "this folder has nowhere to push to yet"
	}
	return string(v)
}
