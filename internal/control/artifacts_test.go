package control

import (
	"crypto/sha256"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestDownloadGrantIsBoundToArtifactAndExpiry(t *testing.T) {
	api := &API{operator: sha256.Sum256([]byte("test operator token"))}
	now := time.Unix(1_700_000_000, 0)
	path := "/api/v1/runs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/artifacts/godot-web.zip"
	expires := strconv.FormatInt(now.Add(downloadGrantDuration).Unix(), 10)
	grant := expires + "." + api.downloadSignature(path, expires)
	for _, tc := range []struct {
		name, path, cookie string
		at                 time.Time
		allowed            bool
	}{
		{"original artifact", path, grant, now, true},
		{"different artifact", "/api/v1/runs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/artifacts/godot.log", grant, now, false},
		{"expired", path, grant, now.Add(downloadGrantDuration), false},
		{"changed expiry", path, strconv.FormatInt(now.Add(time.Hour).Unix(), 10) + grant[len(expires):], now, false},
		{"no grant", path, "", now, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.cookie != "" {
				request.AddCookie(&http.Cookie{Name: downloadCookie, Value: tc.cookie})
			}
			if allowed := api.downloadGranted(request, tc.at); allowed != tc.allowed {
				t.Fatalf("grant accepted = %v, want %v", allowed, tc.allowed)
			}
		})
	}
}
