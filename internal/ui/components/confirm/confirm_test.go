package confirm_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
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

// A click on a button answers the way pressing it would, a click anywhere
// else answers nothing, and Yes stays out of reach until the typed word is in.
func TestClickAnswersOnlyOnTheButtons(t *testing.T) {
	c := uictx.Context{Theme: theme.For(true), Icons: icons.Unicode(), Width: 80, Height: 24, BodyHeight: 20}
	m := confirm.New("x", "Delete 3 folders?", "1.2 GB")
	lines := strings.Split(ansi.Strip(m.View(c)), "\n")
	row := len(lines) - 2
	noX := cellIndex(lines[row], "[ No ]")
	yesX := cellIndex(lines[row], "[ Yes ]")
	if noX < 0 || yesX < 0 {
		t.Fatalf("buttons not on the row above the border: %q", lines[row])
	}

	answer := func(cmd tea.Cmd) (confirm.Answer, bool) {
		if cmd == nil {
			return 0, false
		}
		a, ok := cmd().(confirm.AnsweredMsg)
		return a.Answer, ok
	}
	if _, cmd := m.Click(c, noX+2, row); true {
		if a, ok := answer(cmd); !ok || a != confirm.AnswerNo {
			t.Errorf("click on No = %v,%v", a, ok)
		}
	}
	if _, cmd := m.Click(c, yesX+2, row); true {
		if a, ok := answer(cmd); !ok || a != confirm.AnswerYes {
			t.Errorf("click on Yes = %v,%v", a, ok)
		}
	}
	for _, at := range [][2]int{{yesX + 2, row - 1}, {0, row}, {yesX - 1, row}, {yesX + 40, row}} {
		if _, cmd := m.Click(c, at[0], at[1]); cmd != nil {
			t.Errorf("a click at %v answered the dialog", at)
		}
	}

	typed := confirm.New("x", "Delete?", "").WithTypedWord("DELETE")
	tl := strings.Split(ansi.Strip(typed.View(c)), "\n")
	trow := len(tl) - 2
	if _, cmd := typed.Click(c, cellIndex(tl[trow], "[ Yes ]")+2, trow); cmd != nil {
		t.Error("a click on Yes answered before the word was typed")
	}
}

// cellIndex is the terminal column sub starts at in line, or -1. The card
// border is a multi-byte rune, so a byte offset would land in the wrong cell.
func cellIndex(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(line[:i])
}
