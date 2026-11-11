package winapi

import "testing"

func TestShareInstanceNamesMatchTheirShare(t *testing.T) {
	client := []struct {
		unc, instance string
		want          bool
	}{
		{`\\192.168.1.178\COD-4-ew6q`, `\192.168.1.178\COD-4-ew6q`, true},
		{`\\192.168.1.178\COD-4-ew6q`, `\192.168.1.178\cod-4-ew6q`, true}, // case
		{`\\192.168.1.178\COD-4-ew6q\`, `\192.168.1.178\COD-4-ew6q`, true},
		{`\\192.168.1.178\COD-4-ew6q`, `\192.168.1.178\IPC$`, false},
		{`\\192.168.1.178\COD-4-ew6q`, `\192.168.137.1\COD-4-ew6q`, false},
		{`\\192.168.1.178\COD-4-ew6q`, `_Total`, false},
	}
	for _, c := range client {
		if got := clientInstanceKey(c.unc) == clientInstanceKey(c.instance); got != c.want {
			t.Errorf("client %q vs %q = %v, want %v", c.unc, c.instance, got, c.want)
		}
	}
	server := []struct {
		share, instance string
		want            bool
	}{
		{"Games-ab12", `\\*\Games-ab12`, true},
		{"Games-ab12", `\\*\games-AB12`, true},
		{"Games-ab12", `\\scope\Games-ab12`, true},
		{"Games-ab12", `\\*\C$`, false},
		{"Games-ab12", `_Total`, false},
	}
	for _, c := range server {
		if got := serverInstanceKey(c.share) == serverInstanceKey(c.instance); got != c.want {
			t.Errorf("server %q vs %q = %v, want %v", c.share, c.instance, got, c.want)
		}
	}
	if clientInstanceKey("_Total") != "" || serverInstanceKey("_total") != "" || clientInstanceKey(`\\`) != "" {
		t.Error("_Total or an empty path matched a share")
	}
}

func TestAShareCountOnlyGoesUpAcrossANewInstance(t *testing.T) {
	var tl byteTally
	steps := []struct{ raw, want uint64 }{
		{5000, 5000}, // first reading: what the instance counted before
		{8000, 8000}, // +3000
		{8000, 8000}, // nothing moved
		{200, 8200},  // the session came back: a new instance from zero
		{1200, 9200}, // +1000
	}
	for i, s := range steps {
		if got := tl.add(s.raw); got != s.want {
			t.Fatalf("step %d: add(%d) = %d, want %d", i, s.raw, got, s.want)
		}
	}
}

func TestOpenShareBytesRefusesAnEmptyName(t *testing.T) {
	if _, err := OpenClientShareBytes(`\\`); err == nil {
		t.Error("an empty UNC path opened")
	}
	if _, err := OpenServerShareBytes(" "); err == nil {
		t.Error("an empty share name opened")
	}
}
