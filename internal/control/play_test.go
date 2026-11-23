package control

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebPlayGrantIsBoundToBuildAndOrigin(t *testing.T) {
	a := &API{playHost: "play.example.test"}
	id := strings.Repeat("a", 32)
	digest := strings.Repeat("b", 64)
	now := time.Unix(100, 0)
	expires := strconv.FormatInt(now.Unix()+5, 10)
	grant := digest + "." + expires + "." + a.playSignature(id, digest, expires)
	if got, ok := a.playDigest(id, grant, now); !ok || got != digest {
		t.Fatal("expected authorized digest")
	}
	for _, attempt := range []struct {
		id, grant string
		at        time.Time
	}{
		{strings.Repeat("c", 32), grant, now},
		{id, "a" + grant[1:], now},
		{id, grant, now.Add(5 * time.Second)},
	} {
		if _, ok := a.playDigest(attempt.id, attempt.grant, attempt.at); ok {
			t.Fatal("accepted a modified or expired grant")
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://admin.example.test/play/"+id+"/"+grant+"/index.html", nil)
	r.SetPathValue("id", id)
	a.play(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("served game from the control origin: %d", w.Code)
	}
}
