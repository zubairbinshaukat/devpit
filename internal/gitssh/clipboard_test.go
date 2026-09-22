package gitssh

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestCopy_OSC52Encoding(t *testing.T) {
	var buf bytes.Buffer
	if err := Copy(&buf, "hello clipboard"); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}

	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello clipboard")) + "\x07"
	if buf.String() != want {
		t.Fatalf("Copy() wrote %q, want %q", buf.String(), want)
	}
}

func TestCopy_EmptyString(t *testing.T) {
	var buf bytes.Buffer
	if err := Copy(&buf, ""); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	want := "\x1b]52;c;\x07"
	if buf.String() != want {
		t.Fatalf("Copy() wrote %q, want %q", buf.String(), want)
	}
}

func TestCopy_RoundTripsThroughBase64(t *testing.T) {
	var buf bytes.Buffer
	text := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... user@host"
	if err := Copy(&buf, text); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}

	s := buf.String()
	const prefix = "\x1b]52;c;"
	const suffix = "\x07"
	if len(s) < len(prefix)+len(suffix) || s[:len(prefix)] != prefix || s[len(s)-len(suffix):] != suffix {
		t.Fatalf("Copy() output %q does not match the OSC 52 envelope", s)
	}
	payload := s[len(prefix) : len(s)-len(suffix)]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(decoded) != text {
		t.Fatalf("decoded payload = %q, want %q", decoded, text)
	}
}
