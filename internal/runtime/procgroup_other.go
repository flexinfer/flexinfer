//go:build !unix

package runtime

import (
	"errors"
	"os/exec"
	"syscall"
)

var errNoProcessGroup = errors.New("process groups are not supported on this platform")

func setProcessGroup(*exec.Cmd) {}

func processGroupOf(*exec.Cmd) int { return 0 }

func signalProcessGroup(int, syscall.Signal) error { return errNoProcessGroup }

func processGroupAlive(int) (bool, int) { return false, 0 }
