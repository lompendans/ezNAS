package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginThrottleBlocksAfterFiveFailures(t *testing.T) {
	const remote = "192.0.2.45:54321"
	clearLoginFailures(remote)
	defer clearLoginFailures(remote)

	for i := 0; i < loginFailureLimit; i++ {
		if allowed, _ := loginAttemptAllowed(remote); !allowed {
			t.Fatalf("attempt %d was blocked too early", i+1)
		}
		recordLoginFailure(remote)
	}
	if allowed, retry := loginAttemptAllowed(remote); allowed || retry <= 0 {
		t.Fatalf("expected login to be throttled, allowed=%v retry=%v", allowed, retry)
	}
	clearLoginFailures(remote)
	if allowed, _ := loginAttemptAllowed(remote); !allowed {
		t.Fatal("successful login should clear the throttle bucket")
	}
}

func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		site   string
		want   bool
	}{
		{name: "same origin", origin: "https://nas.example:8443", want: true},
		{name: "different origin", origin: "https://attacker.example", want: false},
		{name: "cross-site fetch metadata", site: "cross-site", want: false},
		{name: "no origin for trusted API client", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://nas.example:8443/api/files", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if got := originAllowed(req); got != tc.want {
				t.Fatalf("originAllowed()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestSecureRequestDetection(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://nas.local/", nil)
	if requestIsSecure(req) {
		t.Fatal("plain HTTP request was marked secure")
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if !requestIsSecure(req) {
		t.Fatal("trusted HTTPS proxy header was not recognized")
	}
}
