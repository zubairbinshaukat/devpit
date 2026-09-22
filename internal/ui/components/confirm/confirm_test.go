package confirm_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
)

// press builds the key message for a single printable character.
func press(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// special builds the key message for a named key.
func special(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// TestDefaultIsAlwaysNo is the safety rule from the PRD, enforced here so it
// cannot be lost in a later refactor. Every way of building a dialog, and
// every way of resetting one, must leave the answer on No.
func TestDefaultIsAlwaysNo(t *testing.T) {
	plain := confirm.New("delete", "Delete 14 folders (2.1 GB)?", "You can reinstall with npm install.")
	if got := plain.Answer(); got != confirm.AnswerNo {
		t.Errorf("a fresh dialog answers %s, want no", got)
	}

	typed := plain.WithTypedWord("DELETE")
	if got := typed.Answer(); got != confirm.AnswerNo {
		t.Errorf("a fresh typed-word dialog answers %s, want no", got)
	}

	// Answer yes, then reset: back to No.
	yes, _ := plain.Update(press("y"))
	if yes.Answer() != confirm.AnswerYes {
		t.Fatal("pressing y should reach yes, otherwise this test proves nothing")
	}
	if got := yes.Reset().Answer(); got != confirm.AnswerNo {
		t.Errorf("Reset left the answer on %s, want no", got)
	}
}

func TestEscAndNAnswerNo(t *testing.T) {
	const keyEsc = 27
	for name, msg := range map[string]tea.KeyPressMsg{
		"n":   press("n"),
		"esc": special(keyEsc),
	} {
		m := confirm.New("x", "Really?", "")
		next, cmd := m.Update(msg)
		if next.Answer() != confirm.AnswerNo {
			t.Errorf("%s left the dialog on yes", name)
		}
		if cmd == nil {
			t.Fatalf("%s produced no message", name)
		}
		ans, ok := cmd().(confirm.AnsweredMsg)
		if !ok {
			t.Fatalf("%s produced %T, want AnsweredMsg", name, cmd())
		}
		if ans.Answer != confirm.AnswerNo {
			t.Errorf("%s reported %s, want no", name, ans.Answer)
		}
		if ans.ID != "x" {
			t.Errorf("AnsweredMsg.ID = %q, want x", ans.ID)
		}
	}
}

func TestEnterOnAFreshDialogAnswersNo(t *testing.T) {
	const keyEnter = 13
	m := confirm.New("x", "Really?", "")
	_, cmd := m.Update(special(keyEnter))
	if cmd == nil {
		t.Fatal("enter produced no message")
	}
	ans := cmd().(confirm.AnsweredMsg)
	if ans.Answer != confirm.AnswerNo {
		t.Errorf("enter on a fresh dialog answered %s, want no", ans.Answer)
	}
}

func TestTypedWordGatesYes(t *testing.T) {
	const keyEnter = 13
	m := confirm.New("careful", "Delete 3 Docker volumes?", "Volumes can hold database data.").
		WithTypedWord("DELETE")

	// A partial word must not unlock yes.
	for _, r := range "DELET" {
		m, _ = m.Update(press(string(r)))
	}
	if m.Answer() != confirm.AnswerNo {
		t.Error("a partial word unlocked yes")
	}

	// y alone must not bypass the word.
	m, _ = m.Update(press("y"))
	if m.Answer() != confirm.AnswerNo {
		t.Error("pressing y bypassed the typed word")
	}

	// Backspace out the stray y, then finish the word.
	const keyBackspace = 127
	m, _ = m.Update(special(keyBackspace))
	m, _ = m.Update(press("E"))
	if m.Typed() != "DELETE" {
		t.Fatalf("typed = %q, want DELETE", m.Typed())
	}
	if m.Answer() != confirm.AnswerYes {
		t.Error("the completed word should unlock yes")
	}

	_, cmd := m.Update(special(keyEnter))
	if cmd == nil {
		t.Fatal("enter produced no message")
	}
	if ans := cmd().(confirm.AnsweredMsg); ans.Answer != confirm.AnswerYes {
		t.Errorf("enter after the word answered %s, want yes", ans.Answer)
	}
}

func TestTypedWordIsCaseSensitive(t *testing.T) {
	m := confirm.New("careful", "Really?", "").WithTypedWord("DELETE")
	for _, r := range "delete" {
		m, _ = m.Update(press(string(r)))
	}
	if m.Answer() != confirm.AnswerNo {
		t.Error("a lower-case word should not unlock yes")
	}
}

func TestWithTypedWordResetsAnyPriorYes(t *testing.T) {
	m := confirm.New("x", "Really?", "")
	m, _ = m.Update(press("y"))
	if m.Answer() != confirm.AnswerYes {
		t.Fatal("setup failed")
	}
	if got := m.WithTypedWord("DELETE").Answer(); got != confirm.AnswerNo {
		t.Errorf("WithTypedWord kept a prior yes: %s", got)
	}
}

func TestAnswerString(t *testing.T) {
	if confirm.AnswerNo.String() != "no" || confirm.AnswerYes.String() != "yes" {
		t.Error("Answer.String is wrong")
	}
}
