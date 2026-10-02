package accounts

import "encoding/json"

// IdentityState is what a who-am-I check found.
type IdentityState string

const (
	StateSignedIn     IdentityState = "signed in"
	StateNotSignedIn  IdentityState = "not signed in"
	StateExpired      IdentityState = "expired"
	StateNotInstalled IdentityState = "not installed"
	StateUnknown      IdentityState = "unknown"
	// StateRecorded: who Devpit recorded at sign-in, import or the last
	// Verify. Nothing was asked of the tool; it is shown as is.
	StateRecorded IdentityState = "recorded"
)

// IdentityFields is what an adapter fills in to build an [Identity].
type IdentityFields struct {
	State IdentityState
	// Email is the account's address.
	Email string
	// Login is a user name or handle (a GitHub login).
	Login string
	// Name is a person's display name (Git's user.name).
	Name string
	// Org is the organisation or team the account belongs to.
	Org string
	// Plan is the subscription or plan name ("max", "pro", "hobby").
	Plan string
	// Method is how the account signed in ("claude.ai", "console").
	Method string
	// Note is a short sentence for a person ("Sign in again").
	Note string
}

// Identity is who a tool says is signed in. It cannot hold a token: its
// fields are unexported, the only way to build one is [NewIdentity], and
// that passes every field through [Scrub].
type Identity struct {
	f IdentityFields
}

// NewIdentity builds an Identity, scrubbing every field.
func NewIdentity(f IdentityFields) Identity {
	if f.State == "" {
		f.State = StateUnknown
	}
	f.Email = Scrub(f.Email)
	f.Login = Scrub(f.Login)
	f.Name = Scrub(f.Name)
	f.Org = Scrub(f.Org)
	f.Plan = Scrub(f.Plan)
	f.Method = Scrub(f.Method)
	f.Note = Scrub(f.Note)
	return Identity{f: f}
}

// Fields returns a copy of the (already scrubbed) fields.
func (i Identity) Fields() IdentityFields { return i.f }

// State is what the check found; StateUnknown for a zero Identity.
func (i Identity) State() IdentityState {
	if i.f.State == "" {
		return StateUnknown
	}
	return i.f.State
}

// SignedIn reports whether someone is signed in.
func (i Identity) SignedIn() bool { return i.f.State == StateSignedIn }

// Email is the account's address, or "".
func (i Identity) Email() string { return i.f.Email }

// Login is the account's handle, or "".
func (i Identity) Login() string { return i.f.Login }

// Who is the shortest useful way to name who is signed in: the email, else
// the login, else the name.
func (i Identity) Who() string {
	switch {
	case i.f.Email != "":
		return i.f.Email
	case i.f.Login != "":
		return i.f.Login
	}
	return i.f.Name
}

// identityJSON is the stable --json shape.
type identityJSON struct {
	State  IdentityState `json:"state"`
	Email  string        `json:"email,omitempty"`
	Login  string        `json:"login,omitempty"`
	Name   string        `json:"name,omitempty"`
	Org    string        `json:"org,omitempty"`
	Plan   string        `json:"plan,omitempty"`
	Method string        `json:"method,omitempty"`
	Note   string        `json:"note,omitempty"`
}

// MarshalJSON writes the fields with stable names for --json output.
func (i Identity) MarshalJSON() ([]byte, error) {
	f := i.f
	if f.State == "" {
		f.State = StateUnknown
	}
	return json.Marshal(identityJSON(f))
}
