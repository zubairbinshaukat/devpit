package gitssh

import (
	"context"

	"github.com/zubairbinshaukat/devpit/internal/gitssh"
)

// Hooks are the engine calls the leaf screens make. Every field is optional:
// a nil one keeps the real function. They exist so a caller that must not
// run git or ssh-keygen or read ~/.ssh, such as the documentation screenshot
// renderer in internal/shots, can drive every state of the screens from demo
// data.
type Hooks struct {
	// Identity reads the global git name and email.
	Identity func(context.Context) (gitssh.Identity, error)
	// SetConfig writes one global git setting.
	SetConfig func(ctx context.Context, key, value string) error
	// Email reads the global git email, used as the key's comment.
	Email func(context.Context) (string, error)
	// KeyPath is where the new key would be written.
	KeyPath string
	// KeyExists reports whether a key sits at a path.
	KeyExists func(string) bool
	// Keygen generates a key.
	Keygen func(context.Context, gitssh.KeygenOptions, gitssh.Options) (gitssh.KeygenResult, error)
	// CopyWin32 puts text on the clipboard.
	CopyWin32 func(string) error
}

// WithHooks replaces the engine calls the hooks name and keeps the rest. It
// applies to every screen this one opens afterwards.
func (m Model) WithHooks(h Hooks) Model {
	m.hooks = h
	return m
}

// identity applies the hooks to the identity screen.
func (h Hooks) identity(s identityModel) identityModel {
	if h.Identity != nil {
		s.identityFn = h.Identity
	}
	return s
}

// setIdentity applies the hooks to the set-identity screen.
func (h Hooks) setIdentity(s setIdentityModel) setIdentityModel {
	if h.Identity != nil {
		s.loadFn = h.Identity
	}
	if h.SetConfig != nil {
		s.setFn = h.SetConfig
	}
	return s
}

// keygen applies the hooks to the key screen.
func (h Hooks) keygen(s keygenModel) keygenModel {
	if h.Email != nil {
		s.emailFn = h.Email
	}
	if h.KeyPath != "" {
		s.path = h.KeyPath
	}
	if h.KeyExists != nil {
		s.existsFn = h.KeyExists
	}
	if h.Keygen != nil {
		s.keygenFn = h.Keygen
	}
	if h.CopyWin32 != nil {
		s.copyWin32 = h.CopyWin32
	}
	return s
}
