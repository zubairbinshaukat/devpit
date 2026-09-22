package network

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestFlushDNS_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only behaviour")
	}

	fr := &fakeRunner{output: "Windows IP Configuration\n\nSuccessfully flushed the DNS Resolver Cache."}
	out, err := FlushDNS(context.Background(), Options{Runner: fr})
	if err != nil {
		t.Fatalf("FlushDNS() error = %v", err)
	}
	if fr.calledName != "ipconfig" {
		t.Fatalf("runner invoked with name %q, want %q", fr.calledName, "ipconfig")
	}
	if len(fr.calledArgs) != 1 || fr.calledArgs[0] != "/flushdns" {
		t.Fatalf("runner args = %v, want [/flushdns]", fr.calledArgs)
	}
	if out == "" {
		t.Fatal("FlushDNS() returned empty output")
	}
}

func TestFlushDNS_NonWindowsUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this asserts the non-Windows stub")
	}

	_, err := FlushDNS(context.Background(), Options{Runner: &fakeRunner{}})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("FlushDNS() error = %v, want errors.ErrUnsupported", err)
	}
}
