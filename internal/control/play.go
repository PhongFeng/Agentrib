package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/PhongFeng/Agentrib/internal/webgame"
)

const playGrantDuration = 2 * time.Hour

func (a *API) playSignature(id, digest, expires string) string {
	mac := hmac.New(sha256.New, a.operator[:])
	_, _ = fmt.Fprintf(mac, "web play\n%s\n%s\n%s", id, digest, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *API) playDigest(id, grant string, now time.Time) (string, bool) {
	digest, tail, ok := strings.Cut(grant, ".")
	if !ok || !shaPattern.MatchString(digest) {
		return "", false
	}
	stamp, sig, ok := strings.Cut(tail, ".")
	until, err := strconv.ParseInt(stamp, 10, 64)
	return digest, ok && err == nil && now.Unix() < until &&
		hmac.Equal([]byte(sig), []byte(a.playSignature(id, digest, stamp)))
}

func (a *API) authorizePlay(w http.ResponseWriter, r *http.Request) {
	if a.playOrigin == "" {
		problem(w, http.StatusServiceUnavailable, "在线试玩尚未配置")
		return
	}
	r.SetPathValue("name", protocol.WebBuildArchive)
	artifact, err := a.runArtifact(r)
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	id := r.PathValue("id")
	bundle, err := webgame.Open(filepath.Join(a.artifacts, id, artifact.Digest))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "此 CI 网页包无法在线打开")
		return
	}
	_ = bundle.Close()
	stamp := strconv.FormatInt(time.Now().Add(playGrantDuration).Unix(), 10)
	grant := artifact.Digest + "." + stamp + "." + a.playSignature(id, artifact.Digest, stamp)
	send(w, http.StatusOK, map[string]string{"url": a.playOrigin + "/play/" + id + "/" + grant + "/index.html"})
}

func (a *API) play(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.playHost == "" || !strings.EqualFold(r.Host, a.playHost) || !idPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	digest, granted := a.playDigest(id, r.PathValue("grant"), time.Now())
	if !granted {
		problem(w, http.StatusForbidden, "试玩链接已失效，请从工作台重新打开")
		return
	}
	bundle, err := webgame.Open(filepath.Join(a.artifacts, id, digest))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer bundle.Close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	bundle.ServeFile(w, r, r.PathValue("file"))
}
