package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func legalGet(t *testing.T, s *server, path string) (int, string, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w.Code, w.Header().Get("Content-Type"), w.Body.String()
}

// The pages name whoever the deployment says runs the instance, escaped like any
// other text, and link to them.
func TestLegalPagesNameTheOperator(t *testing.T) {
	s := &server{operator: operator{Name: "Dana <b>Doe</b>", Email: "dana@example.org"}}
	for _, path := range []string{"/privacy", "/terms"} {
		code, kind, body := legalGet(t, s, path)
		if code != http.StatusOK || !strings.HasPrefix(kind, "text/html") {
			t.Fatalf("%s: %d %s", path, code, kind)
		}
		if !strings.Contains(body, "Dana &lt;b&gt;Doe&lt;/b&gt;") || strings.Contains(body, "<b>Doe</b>") {
			t.Fatalf("%s: the operator's name is missing or not escaped", path)
		}
		if !strings.Contains(body, `href="mailto:dana@example.org"`) {
			t.Fatalf("%s: no contact address", path)
		}
		if strings.Contains(body, "has not set") {
			t.Fatalf("%s: says the operator is not set while it is", path)
		}
	}
}

// Without the variables the pages say so, rather than name nobody in silence;
// that is what a local stack and a fork of the repository show.
func TestLegalPagesSayWhenNoOperatorIsSet(t *testing.T) {
	s := &server{operator: operator{Name: "Only A Name"}}
	for _, path := range []string{"/privacy", "/terms"} {
		_, _, body := legalGet(t, s, path)
		if !strings.Contains(body, "has not set their name and contact address") {
			t.Fatalf("%s: does not say the operator is missing", path)
		}
		if strings.Contains(body, "Only A Name") || strings.Contains(body, "mailto:") {
			t.Fatalf("%s: shows half an operator", path)
		}
	}
}
