package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	code, body := get(internalRoutes())
	if code != http.StatusOK || !strings.Contains(body, "goroutine profile:") {
		t.Fatalf("internal mux answered %d without a goroutine profile", code)
	}

	s := &server{}
	if code, body := get(s.routes()); strings.Contains(body, "goroutine profile:") {
		t.Fatalf("the public router served a goroutine profile (%d)", code)
	}
}
