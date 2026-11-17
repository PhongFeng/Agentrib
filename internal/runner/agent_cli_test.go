package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestDeepSeekHeadlessPromptAndPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell executable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dsh")
	const executable = `#!/bin/sh
set -eu
[ "$1" = --profile ] && [ "$2" = headless ] || exit 2
if [ "$3" = --help ]; then printf '%s\n' 'task text'; exit 0; fi
[ "$3" = --permission-mode ] && [ "$4" = "$EXPECTED_MODE" ] && [ "$5" = --approval-policy ] && [ "$6" = never ] && [ "$7" = - ] || exit 3
[ "$(cat)" = 'Write product docs' ] || exit 4
printf '%s\n' 'Document ready'
`
	if err := os.WriteFile(path, []byte(executable), 0700); err != nil {
		t.Fatal(err)
	}
	agent, _ := protocol.FindAgent("deepseek-harness")
	backend := backendFor(agent, path)
	ctx := context.Background()
	if err := backend.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"workspace-write", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("EXPECTED_MODE", mode)
			var response string
			err := backend.Execute(ctx, dir, "Write product docs", mode == "read-only", func(kind, text string) {
				if kind == "agent" {
					response = text
				}
			})
			if err != nil || response != "Document ready" {
				t.Fatalf("headless result %q: %v", response, err)
			}
		})
	}
}
