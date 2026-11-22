package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapturePreviewsUsesCommittedRoleAssets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	ctx := context.Background()
	repo := t.TempDir()
	dir := filepath.Join(repo, "game")
	runGit := func(args ...string) string {
		t.Helper()
		output, err := git(ctx, repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	runGit("init")
	files := []string{"docs/production/requirements.md", "assets/art/title.png", "assets/audio/sfx/push.wav",
		"assets/audio/master/track.wav", "assets/art/preview.html", "private.md"}
	for _, name := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("saved "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", "--", ".")
	runGit("-c", "user.name=Agentrib", "-c", "user.email=agentrib@localhost", "-c", "commit.gpgsign=false", "commit", "-m", "Assets")
	revision := runGit("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, files[0]), []byte("unsaved"), 0600); err != nil {
		t.Fatal(err)
	}
	for i, step := range []string{"producer", "art", "qa:fix:audio"} {
		out := t.TempDir()
		artifacts, err := capturePreviews(ctx, repo, dir, revision, step, out)
		if err != nil || len(artifacts) != 1 || !strings.HasSuffix(artifacts[0].Name, filepath.Base(files[i])) {
			t.Fatalf("%s preview: %+v %v", step, artifacts, err)
		}
		body, err := os.ReadFile(filepath.Join(out, artifacts[0].Name))
		if err != nil || string(body) != "saved "+files[i] {
			t.Fatalf("%s did not use the committed asset: %q %v", step, body, err)
		}
	}
}
