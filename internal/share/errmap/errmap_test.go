package errmap_test

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
)

// pitfalls are the problems the plan names. Each one has to have an entry
// that says what happened and what to do, so a missing one fails here.
var pitfalls = []string{
	"guest-signing",     // Windows 11 24H2: guest logons off, SMB signing required
	"blank-password",    // blank-password accounts cannot sign in over the network
	"microsoft-account", // needs MicrosoftAccount\email, a PIN does not work
	"path-too-long",     // paths over 260
	"disk-full",         // ERROR 112 mid-copy
	"fat32",             // FAT32 4 GB
	"antivirus",         // antivirus slowing writes
	"sleeping",          // PC sleeping
	"share-removed",     // share removed mid-copy
	"session-conflict",  // net use 1219
	"not-found",         // 53
	"share-not-found",   // 67
	"wrong-password",    // 86, 1326
	"access-denied",     // 5
	"network-dropped",   // 64
}

func TestEveryPitfallHasAnEntryWithWordsAndASteps(t *testing.T) {
	for _, id := range pitfalls {
		e, ok := errmap.ByID(id)
		if !ok {
			t.Errorf("no entry %q", id)
			continue
		}
		if e.Title == "" || e.Why == "" || len(e.Steps) == 0 || e.Source == "" {
			t.Errorf("entry %q is incomplete: %+v", id, e)
		}
	}
}

func TestTheNumbersThePlanNamesAreCovered(t *testing.T) {
	for _, code := range []int{5, 53, 64, 67, 86, 112, 121, 206, 223, 1203, 1219, 1225, 1231, 1326, 1327} {
		if _, ok := errmap.Lookup(code, errmap.PhaseConnect); !ok {
			t.Errorf("error %d has no entry", code)
		}
	}
}

func TestNoNumberBelongsToTwoEntries(t *testing.T) {
	seen := map[int]string{}
	ids := map[string]bool{}
	for _, e := range errmap.Entries() {
		if ids[e.ID] {
			t.Errorf("duplicate id %q", e.ID)
		}
		ids[e.ID] = true
		for _, c := range e.Codes {
			if other, dup := seen[c]; dup {
				t.Errorf("error %d is in both %q and %q", c, other, e.ID)
			}
			seen[c] = e.ID
		}
	}
}

func TestTextIsEasyEnglishAndHasNoJargonOrRawNumbersAsTheOnlyMessage(t *testing.T) {
	for _, e := range errmap.Entries() {
		all := e.Title + " " + e.Why + " " + strings.Join(e.Steps, " ")
		for _, word := range []string{"NTSTATUS", "HRESULT", "0x"} {
			if strings.Contains(all, word) {
				t.Errorf("%q mentions %q", e.ID, word)
			}
		}
		for _, s := range append([]string{e.Why}, e.Steps...) {
			if n := len(strings.Fields(s)); n > 30 {
				t.Errorf("%q has a %d word sentence: %q", e.ID, n, s)
			}
		}
	}
}

func TestEntriesIsACopy(t *testing.T) {
	a := errmap.Entries()
	a[0].Title = "changed"
	if errmap.Entries()[0].Title == "changed" {
		t.Error("Entries returns the table itself")
	}
}

func TestSessionConflictOffersToDropConnections(t *testing.T) {
	e, ok := errmap.Lookup(1219, errmap.PhaseConnect)
	if !ok || e.Action != errmap.ActionDropConnections {
		t.Errorf("1219 = %+v", e)
	}
}

func TestDuringACopyTheWrongAddressNumbersMeanTheOtherPCWentAway(t *testing.T) {
	for _, code := range []int{53, 67} {
		before, _ := errmap.Lookup(code, errmap.PhaseConnect)
		during, _ := errmap.Lookup(code, errmap.PhaseCopy)
		if before.ID == during.ID || during.ID != "share-removed" || !during.Transient {
			t.Errorf("error %d: before %q, during %q", code, before.ID, during.ID)
		}
	}
}

func TestExplainSuggestsTheMicrosoftAccountFormForAWrongPassword(t *testing.T) {
	err := fmt.Errorf("signing in: %w", syscall.Errno(1326))
	e, ok := errmap.Explain(err, errmap.PhaseConnect, "me@example.com")
	if !ok || e.ID != "microsoft-account" {
		t.Errorf("e-mail user = %+v", e)
	}
	e, _ = errmap.Explain(err, errmap.PhaseConnect, `MicrosoftAccount\me@example.com`)
	if e.ID != "wrong-password" {
		t.Errorf("prefixed user = %q, want wrong-password", e.ID)
	}
	e, _ = errmap.Explain(err, errmap.PhaseConnect, "bob")
	if e.ID != "wrong-password" {
		t.Errorf("plain user = %q", e.ID)
	}
}

func TestExplainWithoutANumber(t *testing.T) {
	if _, ok := errmap.Explain(errors.New("plain"), errmap.PhaseConnect, ""); ok {
		t.Error("an error with no number was explained")
	}
}

func TestCodeFromTextIgnoresTheLanguage(t *testing.T) {
	for text, want := range map[string]int{
		"System error 53 has occurred.\n\nThe network path was not found.": 53,
		"Systemfehler 1219 aufgetreten.\n":                                 1219,
		"Erreur système 67 s’est produite.":                                67,
		"システム エラー 1326 が発生しました。":                                           1326,
		"Sistem hatası 86 oluştu.":                                         86,
		"\n\nSystem error 5 has occurred.":                                 5,
	} {
		if got, ok := errmap.CodeFromText(text); !ok || got != want {
			t.Errorf("CodeFromText(%q) = %d, %v; want %d", text, got, ok, want)
		}
	}
	if _, ok := errmap.CodeFromText("no digits at all"); ok {
		t.Error("found a number in text with none")
	}
}

func TestCodeOfFindsAnErrnoInAWrappedError(t *testing.T) {
	err := fmt.Errorf("a: %w", fmt.Errorf("b: %w", syscall.Errno(64)))
	if c, ok := errmap.CodeOf(err); !ok || c != 64 {
		t.Errorf("CodeOf = %d, %v", c, ok)
	}
}
