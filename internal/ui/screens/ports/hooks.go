package ports

import (
	"context"
	"time"

	engine "github.com/zubairbinshaukat/devpit/internal/ports"
)

// Hooks are the engine calls the screen makes. Every field is optional: a nil
// one keeps the real function. They exist so a caller that must not touch the
// machine, such as the documentation screenshot renderer in internal/shots,
// can drive every state of the screen from demo data.
type Hooks struct {
	// List returns every connection on the machine.
	List func(context.Context) ([]engine.Conn, error)
	// ByPort returns the connections on one port.
	ByPort func(uint16) ([]engine.Conn, error)
	// Lookup describes one process.
	Lookup func(uint32) (engine.Process, error)
	// Tree returns a process's descendants.
	Tree func(uint32) []engine.Process
	// Kill stops one process.
	Kill func(uint32) error
	// KillTree stops a process and its descendants.
	KillTree func(uint32) error
	// WaitFree waits for a port to go quiet.
	WaitFree func(context.Context, uint16, time.Duration) bool
}

// WithHooks replaces the engine calls the hooks name and keeps the rest.
func (m Model) WithHooks(h Hooks) Model {
	if h.List != nil {
		m.listFn = h.List
	}
	if h.ByPort != nil {
		m.byPortFn = h.ByPort
	}
	if h.Lookup != nil {
		m.lookupFn = h.Lookup
	}
	if h.Tree != nil {
		m.treeFn = h.Tree
	}
	if h.Kill != nil {
		m.killFn = h.Kill
	}
	if h.KillTree != nil {
		m.killTreeFn = h.KillTree
	}
	if h.WaitFree != nil {
		m.waitFreeFn = h.WaitFree
	}
	return m
}
