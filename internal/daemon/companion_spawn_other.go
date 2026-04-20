//go:build !darwin

package daemon

import (
	"os/exec"
	"syscall"
)

// spawnCompanionWrapper on non-Darwin platforms uses plain fork+exec via
// exec.Cmd, with Setsid so the wrapper survives daemon death for hot reload.
// There is no responsible-process concept outside macOS, so no disclaim.
func spawnCompanionWrapper(path string, argv []string, envv []string, workdir string) (*exec.Cmd, error) {
	cmd := &exec.Cmd{
		Path:        path,
		Args:        argv,
		Env:         envv,
		Dir:         workdir,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
