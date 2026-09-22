package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc lets a test supply canned HTTP responses without touching
// the real network or DNS.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newFakeClient(f roundTripFunc) *http.Client {
	return &http.Client{Transport: f}
}

var errFakeDialFailed = errors.New("fake: dial failed")

func TestPublicIP_PrimaryServiceAnswers(t *testing.T) {
	client := newFakeClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.ipify.org" {
			t.Fatalf("unexpected request to %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("203.0.113.7")),
			Header:     make(http.Header),
		}, nil
	})

	ip, err := PublicIP(context.Background(), client)
	if err != nil {
		t.Fatalf("PublicIP() error = %v", err)
	}
	if ip.String() != "203.0.113.7" {
		t.Fatalf("PublicIP() = %s, want 203.0.113.7", ip)
	}
}

func TestPublicIP_FallsBackToSecondService(t *testing.T) {
	client := newFakeClient(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://api.ipify.org":
			return nil, errFakeDialFailed
		case "https://ifconfig.me/ip":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("198.51.100.9\n")),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request to %s", req.URL)
			return nil, nil
		}
	})

	ip, err := PublicIP(context.Background(), client)
	if err != nil {
		t.Fatalf("PublicIP() error = %v", err)
	}
	if ip.String() != "198.51.100.9" {
		t.Fatalf("PublicIP() = %s, want 198.51.100.9", ip)
	}
}

func TestPublicIP_BothServicesDownIsOffline(t *testing.T) {
	client := newFakeClient(func(_ *http.Request) (*http.Response, error) {
		return nil, errFakeDialFailed
	})

	_, err := PublicIP(context.Background(), client)
	if err == nil {
		t.Fatal("PublicIP() error = nil, want *OfflineError")
	}
	var offline *OfflineError
	if !errors.As(err, &offline) {
		t.Fatalf("PublicIP() error = %v (%T), want *OfflineError", err, err)
	}
}
