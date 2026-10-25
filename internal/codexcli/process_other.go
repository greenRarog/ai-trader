//go:build !linux

package codexcli

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) { cmd.WaitDelay = time.Second }
