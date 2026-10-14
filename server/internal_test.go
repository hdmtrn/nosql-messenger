package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// A profile shows the goroutines, the heap and the command line of the
// process. The internal mux serves them; the public router must not, whatever
// it answers instead.
func TestProfilesOnlyOnTheInternalMux(t *testing.T) {
	get := func(h http.Handler) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil))
		return rec.Code, rec.Body.String()
	}

	code, body := get((&server{}).internalRoutes())
	if code != http.StatusOK || !strings.Contains(body, "goroutine profile:") {
		t.Fatalf("internal mux answered %d without a goroutine profile", code)
	}

	s := &server{}
	if code, body := get(s.routes()); strings.Contains(body, "goroutine profile:") {
		t.Fatalf("the public router served a goroutine profile (%d)", code)
	}
}

// The socket gauge reads the hub when scraped, and the public router never
// serves the metrics, which name nobody but still describe the node.
func TestMetricsCountSocketsOnlyOnTheInternalMux(t *testing.T) {
	get := func(h http.Handler) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rec.Code, rec.Body.String()
	}

	s := &server{hub: NewHub()}
	s.hub.Connect(newSubscriber("ann"), nil)
	s.hub.Connect(newSubscriber("ann"), nil)
	s.hub.Connect(newSubscriber("bob"), nil)

	code, body := get(s.internalRoutes())
	if code != http.StatusOK {
		t.Fatalf("internal mux answered %d", code)
	}
	for _, want := range []string{"\nmessenger_sockets 3\n", "\nmessenger_users_connected 2\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics lack %q", strings.TrimSpace(want))
		}
	}

	if _, body := get(s.routes()); strings.Contains(body, "messenger_sockets") {
		t.Fatal("the public router served the metrics")
	}
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics answered %d", rec.Code)
	}
	return rec.Body.String()
}

// A socket that stops reading fills its buffer, and the frame after that drops
// it: the scrape tells frames handed over from sockets thrown away.
func TestMetricsCountFramesAndSlowSockets(t *testing.T) {
	hub := NewHub()
	slow := newSubscriber("ann")
	hub.Connect(slow, []string{"ch"})
	for range sendBuffer + 1 {
		hub.Publish("ch", []byte("{}"))
	}

	body := scrape(t, metricsHandler(newMetrics(hub, nil, nil)))
	for _, want := range []string{
		fmt.Sprintf("\nmessenger_frames_sent_total %d\n", sendBuffer),
		"\nmessenger_sockets_dropped_slow_total 1\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics lack %q", strings.TrimSpace(want))
		}
	}
}

// A publish Redis refuses and a gap need no Redis to be counted: the first
// goes to an address nothing listens on, the second is called directly.
func TestMetricsCountFailedPublishesAndGaps(t *testing.T) {
	b := &bus{
		hub: NewHub(),
		rdb: redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond}),
	}
	defer b.rdb.Close()

	b.Publish("ch", []byte("{}"))
	b.gap()

	body := scrape(t, metricsHandler(newMetrics(nil, b, nil)))
	for _, want := range []string{
		"\nmessenger_bus_publish_failures_total 1\n",
		"\nmessenger_bus_published_total 0\n",
		"\nmessenger_bus_gaps_total 1\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics lack %q", strings.TrimSpace(want))
		}
	}
}
