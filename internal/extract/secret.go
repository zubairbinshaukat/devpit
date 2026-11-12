package extract

import "errors"

// Secret holds a password in memory. It never prints and refuses to marshal,
// so it cannot reach a log, a record or a frame by accident (rule 79). The
// only way out is [childEnv], which puts it in hatchzip's own environment.
type Secret struct{ v string }

// NewSecret wraps s.
func NewSecret(s string) Secret { return Secret{v: s} }

// Empty reports whether no password is held.
func (s Secret) Empty() bool { return s.v == "" }

// String hides the value.
func (Secret) String() string { return "(hidden)" }

// GoString hides the value from %#v.
func (Secret) GoString() string { return "(hidden)" }

// errSecretMarshal is returned by every marshal attempt.
var errSecretMarshal = errors.New("extract: a password is never marshalled")

// MarshalJSON always fails.
func (Secret) MarshalJSON() ([]byte, error) { return nil, errSecretMarshal }

// MarshalText always fails.
func (Secret) MarshalText() ([]byte, error) { return nil, errSecretMarshal }
