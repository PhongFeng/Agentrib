package control

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuestPermissions(t *testing.T) {
	handler := New(nil, "operator-test-token", t.TempDir(), "")
	for _, route := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/v1/public/overview", http.StatusMethodNotAllowed},
		{"GET", "/api/v1/machines", http.StatusUnauthorized},
		{"GET", "/api/v1/runs/example", http.StatusUnauthorized},
		{"POST", "/api/v1/executions", http.StatusUnauthorized},
		{"POST", "/api/v1/executions/example/approve/step", http.StatusUnauthorized},
		{"POST", "/api/v1/runs/example/cancel", http.StatusUnauthorized},
		{"POST", "/api/v1/runs/example/artifacts/file.zip", http.StatusUnauthorized},
		{"POST", "/api/v1/runner/claim", http.StatusUnauthorized},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != route.want {
			t.Errorf("%s %s: got %d, want %d", route.method, route.path, w.Code, route.want)
		}
	}
}
