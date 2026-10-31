package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	loginFailureWindow = 15 * time.Minute
	loginFailureLimit  = 5
	maxLoginBuckets    = 4096
)

type loginBucket struct {
	started  time.Time
	failures int
}

var (
	loginLimitMu sync.Mutex
	loginBuckets = make(map[string]loginBucket)
)

// clientIP deliberately uses RemoteAddr instead of forwarded headers. Forwarded
// headers are only trustworthy when ezNAS is behind a proxy the owner controls.
func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return strings.Trim(remoteAddr, "[]")
}

func loginAttemptAllowed(remoteAddr string) (bool, time.Duration) {
	ip := clientIP(remoteAddr)
	if ip == "" {
		return false, loginFailureWindow
	}
	now := time.Now()
	loginLimitMu.Lock()
	defer loginLimitMu.Unlock()

	bucket, ok := loginBuckets[ip]
	if !ok {
		if len(loginBuckets) >= maxLoginBuckets {
			for key, old := range loginBuckets {
				if now.Sub(old.started) >= loginFailureWindow {
					delete(loginBuckets, key)
				}
			}
			if len(loginBuckets) >= maxLoginBuckets {
				return false, loginFailureWindow
			}
		}
		loginBuckets[ip] = loginBucket{started: now, failures: 1}
		return true, 0
	}
	if now.Sub(bucket.started) >= loginFailureWindow {
		loginBuckets[ip] = loginBucket{started: now, failures: 1}
		return true, 0
	}
	if bucket.failures >= loginFailureLimit {
		retry := loginFailureWindow - now.Sub(bucket.started)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}
	// Reserve the attempt while holding the mutex so parallel requests cannot
	// bypass the limit before password hashing finishes.
	bucket.failures++
	loginBuckets[ip] = bucket
	return true, 0
}

func clearLoginFailures(remoteAddr string) {
	ip := clientIP(remoteAddr)
	loginLimitMu.Lock()
	delete(loginBuckets, ip)
	loginLimitMu.Unlock()
}

// originAllowed blocks browser-based cross-origin writes. Requests without an
// Origin or Referer are retained for trusted command-line/API clients.
func originAllowed(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	raw := r.Header.Get("Origin")
	if raw == "" {
		raw = r.Referer()
	}
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// This is useful behind a TLS-terminating reverse proxy. Only configure
	// trusted proxies; direct clients can otherwise supply this header.
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}
