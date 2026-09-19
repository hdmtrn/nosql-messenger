package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

// The close code is the only thing that tells the client a refusal from a
// network drop; without it a revoked tab reconnects forever. A drop for a slow
// reader must stay a plain close, which the client answers by reconnecting.
func TestCloseCodeTellsARevocationFromADrop(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(h *Hub, c *Subscriber)
		want  int
	}{
		{"revoked", func(h *Hub, c *Subscriber) { h.CloseSession("s1") }, closeSessionRevoked},
		{"dropped", func(h *Hub, c *Subscriber) { h.Disconnect(c) }, websocket.CloseNoStatusReceived},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub()
			c := newSubscriber("u1")
			c.sessionID = "s1"
			h.Connect(c, nil)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Upgrade(w, r, nil, 0, 0)
				if err != nil {
					return
				}
				c.writePump(conn)
			}))
			defer srv.Close()

			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatalf("dialing: %v", err)
			}
			defer conn.Close()

			tc.close(h, c)

			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _, err = conn.ReadMessage()
			var ce *websocket.CloseError
			if !errors.As(err, &ce) {
				t.Fatalf("read gave %v, want a close frame", err)
			}
			if ce.Code != tc.want {
				t.Fatalf("close code %d, want %d", ce.Code, tc.want)
			}
			if ce.Text != closeReasons[tc.want] {
				t.Fatalf("close reason %q, want %q", ce.Text, closeReasons[tc.want])
			}
		})
	}
}
