//go:build windows

package shims

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestRegistryPathKeepsTheValueType runs the real registry code against a
// scratch key under HKCU\Software, never the person's real PATH.
func TestRegistryPathKeepsTheValueType(t *testing.T) {
	var b [6]byte
	_, _ = rand.Read(b[:])
	key := `Software\DevpitTest-` + hex.EncodeToString(b[:])
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, key) })
	r := RegistryPath{Root: registry.CURRENT_USER, Key: key, Value: "Path"}

	if _, _, exists, err := r.Read(); err != nil || exists {
		t.Fatalf("fresh key: exists=%v err=%v", exists, err)
	}
	if err := r.Write(`%USERPROFILE%\bin;C:\x`, true); err != nil {
		t.Fatal(err)
	}
	v, expand, exists, err := r.Read()
	if err != nil || !exists || !expand || v != `%USERPROFILE%\bin;C:\x` {
		t.Fatalf("read back: %q expand=%v exists=%v err=%v", v, expand, exists, err)
	}
	if err := r.Write(`C:\y`, false); err != nil {
		t.Fatal(err)
	}
	if v, expand, _, _ := r.Read(); expand || v != `C:\y` {
		t.Fatalf("REG_SZ: %q expand=%v", v, expand)
	}

	m := &Manager{Dir: `C:\shims`, Record: t.TempDir() + `\shims.json`, Path: r}
	if changed, err := m.AddToPath(); err != nil || !changed {
		t.Fatal(err)
	}
	if v, expand, _, _ := r.Read(); v != `C:\shims;C:\y` || expand {
		t.Fatalf("after add: %q expand=%v", v, expand)
	}
}
