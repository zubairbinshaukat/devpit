package winapi

import "testing"

func TestSameGUIDIgnoresCaseAndBraces(t *testing.T) {
	const a = "{79ACB1E1-233C-4CA3-94F7-F4FEF1EBA2A8}"
	for _, b := range []string{a, "{79acb1e1-233c-4ca3-94f7-f4fef1eba2a8}", "79ACB1E1-233C-4CA3-94F7-F4FEF1EBA2A8"} {
		if !sameGUID(a, b) {
			t.Errorf("sameGUID(%q, %q) = false", a, b)
		}
	}
	if sameGUID(a, "{79ACB1E1-233C-4CA3-94F7-F4FEF1EBA2A9}") {
		t.Error("different GUIDs matched")
	}
	if sameGUID("", "") || sameGUID("{}", "{}") {
		t.Error("empty GUIDs matched")
	}
}
