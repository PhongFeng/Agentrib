//go:build windows

package runner

import (
	"os/exec"
	"strconv"
)

func isolate(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill.exe", "/pid", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
