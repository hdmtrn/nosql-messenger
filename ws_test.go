package main

import (
	"net/http/httptest"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	// extraOrigins is read from the environment when the package initialises,
	// so in a test it is simpler to overwrite the variable than to re-run init.
	extraOrigins = []string{"http://localhost:5173"}

	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no Origin means not a browser, let through", "example.com", "", true},
		{"own host", "example.com", "https://example.com", true},
		{"own host in different case", "example.com", "https://EXAMPLE.com", true},
		{"own host with port", "localhost:8080", "http://localhost:8080", true},
		{"foreign host", "example.com", "https://evil.com", false},
		{"a subdomain is a foreign host", "example.com", "https://a.example.com", false},
		{"null from a sandbox", "example.com", "null", false},
		{"dev server from the allow list", "localhost:8080", "http://localhost:5173", true},
		{"same host but another scheme, exact match only", "localhost:8080", "https://localhost:5173", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ws", nil)
			r.Host = c.host
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if got := checkOrigin(r); got != c.want {
				t.Errorf("checkOrigin(Host=%q, Origin=%q) = %v, want %v",
					c.host, c.origin, got, c.want)
			}
		})
	}
}
