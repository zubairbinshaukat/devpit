package claudeshare

import (
	"fmt"
	"strings"
)

// Status is where one shared or copied thing stands in an account.
type Status string

// Statuses.
const (
	// StatusShared: linked to the default account's copy.
	StatusShared Status = "shared"
	// StatusCopy: the account's own copy, the same as the default's now.
	StatusCopy Status = "copy"
	// StatusDrifted: a copy Devpit made that now differs from the
	// default's.
	StatusDrifted Status = "drifted"
	// StatusOwn: the account's own, which differs from the default's and
	// was not copied by Devpit.
	StatusOwn Status = "own"
	// StatusBroken: a link whose target is gone. Repair offers a fix.
	StatusBroken Status = "broken"
	// StatusNotShared: a skill made inside this account that is not shared
	// yet ("1 new skill in work is not shared yet").
	StatusNotShared Status = "not-shared"
	// StatusKeptLocal: kept as the account's own on purpose.
	StatusKeptLocal Status = "kept-local"
	// StatusForeign: a link made by something else; left alone.
	StatusForeign Status = "foreign-link"
	// StatusMissing: the default account has it, this account does not.
	StatusMissing Status = "missing"
)

// ItemStatus is one line of an account's sharing status.
type ItemStatus struct {
	Kind   Kind
	Name   string
	Path   string
	Status Status
	// Detail is a plain sentence.
	Detail string
	// Actions the account page can offer for this line.
	CanStopSharing, CanShare, CanRepair bool
}

// StatusOf lists where every shared, copied or local item of the account
// stands. It reads; it changes nothing.
func StatusOf(r Roots) ([]ItemStatus, error) {
	inv, err := Scan(r)
	if err != nil {
		return nil, err
	}
	return inv.Status(), nil
}

// Status lists where every item stands, from an inventory already taken.
func (inv *Inventory) Status() []ItemStatus {
	var out []ItemStatus
	acct := inv.Roots.TargetName
	for _, it := range inv.Items {
		if it.Locked {
			continue
		}
		for _, e := range it.Entries {
			s := ItemStatus{Kind: it.Kind, Name: e.Name, Path: e.TargetPath}
			copyKey := copiedKey(it.Kind, e.Name)
			switch e.State {
			case EntryLinked:
				s.Status, s.CanStopSharing = StatusShared, it.Kind != KindHistory
				s.Detail = "Shared with the default account."
			case EntryBroken:
				s.Status, s.CanRepair = StatusBroken, true
				s.Detail = "Links to " + e.LinkTarget + ", which is gone."
			case EntryForeignLink:
				s.Status = StatusForeign
				s.Detail = "A link made by something else (to " + e.LinkTarget + "). Devpit leaves it alone."
			case EntrySame:
				s.Status = StatusCopy
				s.CanShare = shareable(it.Kind)
				s.Detail = "Its own copy, the same as the default's."
			case EntryDiffers:
				s.Status, s.Detail = StatusOwn, "Its own, different from the default's."
				if inv.state.wasCopied(copyKey) {
					s.Status, s.Detail = StatusDrifted, "A copy that has changed since (here or in the default account)."
				}
				s.CanShare = shareable(it.Kind)
			case EntryTargetOnly:
				s.Status, s.Detail = StatusOwn, "Only in "+acct+"."
				if it.Kind == KindSkills {
					s.Status, s.Detail, s.CanShare = StatusNotShared, "New in "+acct+", not shared yet.", true
				}
			case EntryKeptLocal:
				s.Status, s.Detail, s.CanShare = StatusKeptLocal, "Kept as "+acct+"'s own.", it.Kind == KindSkills
			case EntrySourceOnly:
				s.Status, s.Detail = StatusMissing, "Only in the default account."
				s.CanShare = shareable(it.Kind)
				s.Path = e.SourcePath
			}
			out = append(out, s)
		}
	}
	return out
}

func shareable(k Kind) bool {
	return k == KindSkills || k == KindAgents || k == KindCommands || k == KindClaudeMD
}

func copiedKey(k Kind, name string) string {
	switch k {
	case KindSkills:
		return "skills/" + name
	case KindAgents:
		return "agents"
	case KindCommands:
		return "commands"
	case KindClaudeMD:
		return "claude-md"
	}
	return string(k)
}

// NotSharedYet is the account page's one-line notice about skills made in
// the account that are not shared ("1 new skill in work is not shared
// yet"), or "" when there are none.
func (inv *Inventory) NotSharedYet() string {
	it, ok := inv.Item(KindSkills)
	if !ok {
		return ""
	}
	var names []string
	for _, e := range it.Entries {
		if e.State == EntryTargetOnly {
			names = append(names, e.Name)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("1 new skill in %s is not shared yet: %s", inv.Roots.TargetName, names[0])
	}
	return fmt.Sprintf("%d new skills in %s are not shared yet: %s", len(names), inv.Roots.TargetName, strings.Join(names, ", "))
}
