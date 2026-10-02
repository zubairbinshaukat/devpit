package accounts

import "github.com/zubairbinshaukat/devpit/internal/protect"

// Protected is the whole list of folders Clean must never touch: the
// built-in login folders (protect.Default) plus every folder Accounts knows
// holds a sign-in or Devpit's own rules ([ProtectedRoots]).
//
// The dependency runs this way on purpose: Accounts knows protect, protect
// knows nothing of Accounts, so scan and clean never pull the accounts
// engine in. The Clean screen passes the result as scan.Options.Protect and
// clean.Options.Protect (both already add protect.Default themselves, so
// this only ever widens what is refused).
func Protected(env protect.LookupEnv) protect.List {
	return protect.Default(env).With(ProtectedRoots()...)
}
