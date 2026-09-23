package app

import (
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Router is the screen stack. The bottom of the stack is home (or, on a fresh
// install, first run) and is never popped: Esc on the root screen does
// nothing rather than leaving the user on a blank frame.
//
// Screens are values, so pushing one and popping back to it restores exactly
// the state it had, cursor included.
//
// A screen that is discarded is always torn down first: Pop, Replace and
// Reset call [uictx.Stopper.Stop] on everything they drop, so a screen that
// started a goroutine cannot keep it running after the user has walked away
// from it.
type Router struct {
	stack []uictx.Screen
}

// NewRouter returns a router with root at the bottom of the stack.
func NewRouter(root uictx.Screen) *Router {
	return &Router{stack: []uictx.Screen{root}}
}

// Len is the number of screens on the stack.
func (r *Router) Len() int { return len(r.stack) }

// Top returns the visible screen and whether there is one.
func (r *Router) Top() (uictx.Screen, bool) {
	if len(r.stack) == 0 {
		return nil, false
	}
	return r.stack[len(r.stack)-1], true
}

// At returns the screen at depth i, counting from the root at 0, and whether
// there is one. The header uses depth 1 to work out which section is open.
func (r *Router) At(i int) (uictx.Screen, bool) {
	if i < 0 || i >= len(r.stack) {
		return nil, false
	}
	return r.stack[i], true
}

// Push adds a screen. A nil screen is ignored so a mis-wired command cannot
// blank the UI.
func (r *Router) Push(s uictx.Screen) {
	if s == nil {
		return
	}
	r.stack = append(r.stack, s)
}

// Pop removes the top screen and reports whether it did. The root screen is
// never popped.
func (r *Router) Pop() bool {
	if len(r.stack) <= 1 {
		return false
	}
	uictx.Stop(r.stack[len(r.stack)-1])
	r.stack = r.stack[:len(r.stack)-1]
	return true
}

// Replace swaps the top screen without changing the stack depth.
func (r *Router) Replace(s uictx.Screen) {
	if s == nil {
		return
	}
	if len(r.stack) == 0 {
		r.stack = []uictx.Screen{s}
		return
	}
	uictx.Stop(r.stack[len(r.stack)-1])
	r.stack[len(r.stack)-1] = s
}

// Reset empties the stack and starts again from root.
func (r *Router) Reset(root uictx.Screen) {
	if root == nil {
		return
	}
	for _, s := range r.stack {
		uictx.Stop(s)
	}
	r.stack = []uictx.Screen{root}
}

// SetTop stores the new value of the visible screen after its Update ran.
func (r *Router) SetTop(s uictx.Screen) {
	if s == nil || len(r.stack) == 0 {
		return
	}
	r.stack[len(r.stack)-1] = s
}

// Breadcrumb returns the titles of the screens above the root, for the header.
func (r *Router) Breadcrumb() string {
	if len(r.stack) == 0 {
		return ""
	}
	return r.stack[len(r.stack)-1].Title()
}
