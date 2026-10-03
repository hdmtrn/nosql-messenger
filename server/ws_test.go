package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.mongodb.org/mongo-driver/v2/bson"
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
		{"revoked", func(h *Hub, c *Subscriber) { h.CloseSession("s1") }, closeSessionEnded},
		{"dropped", func(h *Hub, c *Subscriber) { h.Disconnect(c) }, websocket.CloseNoStatusReceived},
		{"shutdown", func(h *Hub, c *Subscriber) { h.CloseAll(closeServiceRestart) }, closeServiceRestart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub()
			c := newSubscriber("u1")
			c.sessionID = "s1"
			h.Connect(c, nil)

			conn := pumpedSocket(t, c, time.Now().Add(time.Hour), nil)

			tc.close(h, c)

			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _, err := conn.ReadMessage()
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

// Through the router, as a browser connects. A socket with no session must end
// in 4001, not in an HTTP 401 before the upgrade: the browser reports that as
// 1006, like a network drop, and the client would reconnect forever.
func TestSocketWithoutASessionIsClosedWith4001(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	sessions := newSessionStore(db)
	if err := sessions.ensureIndexes(ctx); err != nil {
		t.Fatalf("session indexes: %v", err)
	}
	h := NewHub()
	s := &server{sessions: sessions, channels: newChannelStore(db), hub: h, bus: h}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	user := &User{ID: bson.NewObjectID(), Username: "alice"}
	live, err := sessions.Create(ctx, user, "a browser")
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	gone, err := sessions.Create(ctx, user, "another browser")
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	if err := sessions.Revoke(ctx, user.ID, gone.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	dial := func(t *testing.T, token string) *websocket.Conn {
		t.Helper()
		header := http.Header{}
		if token != "" {
			header.Set("Cookie", sessionCookie+"="+token)
		}
		conn, _, err := websocket.DefaultDialer.Dial(url, header)
		if err != nil {
			t.Fatalf("the upgrade was refused (%v); the check must come after it", err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(time.Second))
		return conn
	}

	for _, tc := range []struct{ name, token string }{
		{"no cookie", ""},
		{"unknown token", "not-a-token"},
		{"revoked", gone.Token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := dial(t, tc.token).ReadMessage()
			var ce *websocket.CloseError
			if !errors.As(err, &ce) || ce.Code != closeSessionEnded {
				t.Fatalf("read gave %v, want close %d", err, closeSessionEnded)
			}
		})
	}

	t.Run("live", func(t *testing.T) {
		conn := dial(t, live.Token)
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, _, err := conn.ReadMessage()
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			t.Fatalf("a live session was closed with %d", ce.Code)
		}
	})
}

// pumpedSocket runs c's write pump behind a real WebSocket and returns the
// client end, so a test reads exactly what a browser would.
func pumpedSocket(t *testing.T, c *Subscriber, expiresAt time.Time, recheck func() (time.Time, error)) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(w, r, nil, 0, 0)
		if err != nil {
			return
		}
		c.writePump(conn, expiresAt, recheck)
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// A socket authenticates once, so an expiry that nobody looks up would leave
// it open for good. When the session is due the pump asks again: gone means
// 4001, extended means carry on, and a database that did not answer is no
// reason to sign anyone out.
func TestSocketClosesWhenItsSessionRunsOut(t *testing.T) {
	for _, tc := range []struct {
		name    string
		recheck func() (time.Time, error)
		closed  bool
	}{
		{"gone", func() (time.Time, error) { return time.Time{}, errSessionNotFound }, true},
		{"extended", func() (time.Time, error) { return time.Now().Add(time.Hour), nil }, false},
		{"database down", func() (time.Time, error) { return time.Time{}, errors.New("no reply") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := make(chan struct{}, 1)
			recheck := func() (time.Time, error) {
				select {
				case asked <- struct{}{}:
				default:
				}
				return tc.recheck()
			}
			conn := pumpedSocket(t, newSubscriber("u1"), time.Now().Add(-time.Second), recheck)

			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, _, err := conn.ReadMessage()
			var ce *websocket.CloseError
			isClosed := errors.As(err, &ce)
			if isClosed != tc.closed {
				t.Fatalf("read gave %v; closed = %v, want %v", err, isClosed, tc.closed)
			}
			if isClosed && ce.Code != closeSessionEnded {
				t.Fatalf("close code %d, want %d", ce.Code, closeSessionEnded)
			}
			select {
			case <-asked:
			default:
				t.Fatal("the pump never asked about the expired session")
			}
		})
	}
}

// A deploy stops a node with SIGTERM. Its sockets must hear 1012, which sends a
// client to another node at once, and the node must take no new ones: the
// listener goes before the sockets are told, so a client that reconnects at
// once cannot land on it again. drain returns only after the socket handlers
// have, since they still use Redis and MongoDB.
func TestShutdownTellsSocketsToReconnect(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	h := NewHub()
	s := &server{sessions: newSessionStore(db), channels: newChannelStore(db), hub: h, bus: h}

	// Logging its going is the last thing a handler does at a shutdown, and
	// drain must not return before it: Redis and MongoDB close right after.
	var logged lockedBuffer
	prev := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(prev) })

	hs, url, header, _ := serveOneSocket(t, s)
	conn := dialWith(t, url, header)

	// The upgrade answers before the handler puts the socket in the hub; a
	// shutdown in between would test the refusal, not the close.
	waitUntil(t, "the socket reached the hub", func() bool { return len(h.Subscribers()) == 1 })

	drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.drain(drainCtx, hs); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !strings.Contains(logged.String(), "- alice disconnected") {
		t.Fatal("drain returned before the socket's handler did")
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != closeServiceRestart {
		t.Fatalf("read gave %v, want close %d", err, closeServiceRestart)
	}

	if again, _, err := websocket.DefaultDialer.Dial(url, header); err == nil {
		again.Close()
		t.Fatal("a stopped node took a new socket")
	}
}

// A node that is shutting down announces nobody offline: its users are back on
// another node within a second, and the announcement would flash them offline
// and online to everyone. Their sockets stay in presence until the node's pulse
// runs out and the sweep of dead nodes takes them.
func TestShutdownLeavesPresenceToTheSweep(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	hub, bus := testInstance(t)
	p := newPresence(bus.rdb)
	s := &server{sessions: newSessionStore(db), channels: newChannelStore(db), users: newUserStore(db),
		hub: hub, bus: bus, presence: p}

	hs, url, header, userID := serveOneSocket(t, s)
	key := presenceSocketsKey + userID
	t.Cleanup(func() {
		bus.rdb.Del(ctx, key, presenceNodeKey+p.nodeID, presenceVersionKey+userID)
	})

	dialWith(t, url, header)
	waitUntil(t, "presence holds the socket", func() bool { return bus.rdb.SCard(ctx, key).Val() == 1 })

	drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.drain(drainCtx, hs); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if n, err := bus.rdb.SCard(ctx, key).Result(); err != nil || n != 1 {
		t.Fatalf("presence holds %d sockets after the shutdown (%v), want 1 left for the sweep", n, err)
	}
}

// serveOneSocket starts s as main would and returns it with the address of its
// socket, the cookie of a fresh session to open it with, and that user's id.
func serveOneSocket(t *testing.T, s *server) (*http.Server, string, http.Header, string) {
	t.Helper()
	ctx := context.Background()

	if err := s.sessions.ensureIndexes(ctx); err != nil {
		t.Fatalf("session indexes: %v", err)
	}
	user := &User{ID: bson.NewObjectID(), Username: "alice"}
	sess, err := s.sessions.Create(ctx, user, "a browser")
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	header := http.Header{}
	header.Set("Cookie", sessionCookie+"="+sess.Token)

	hs := s.httpServer("")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })

	return hs, "ws://" + ln.Addr().String() + "/ws", header, user.ID.Hex()
}

func dialWith(t *testing.T, url string, header http.Header) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// lockedBuffer takes log output from the handler goroutines while the test
// reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
