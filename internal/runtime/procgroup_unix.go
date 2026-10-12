//go:build unix

package runtime

import (
	"errors"
	"os/exec"
	"syscall"
)

// errNoProcessGroup is returned when a backend was not started in its own
// process group (or the group is the runtime's own), so group signals would be
// unsafe.
var errNoProcessGroup = errors.New("backend has no dedicated process group")

// setProcessGroup starts the backend as the leader of a new process group so
// unload can signal every process it spawned (vLLM engine workers, `ollama
// runner` children) and not only the direct child. A group-wide SIGKILL is also
// installed as the context-cancel action, replacing exec's direct-child Kill.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// processGroupOf returns the process group ID of a backend started via
// setProcessGroup (its PID, as the group leader), or 0 when the command was not
// started in its own group.
func processGroupOf(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pgid != 0 {
		return 0
	}
	return cmd.Process.Pid
}

// signalProcessGroup sends sig to every process in pgid. It refuses pgid <= 1
// and the runtime's own group so a bookkeeping bug can never signal the runtime.
func signalProcessGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return errNoProcessGroup
	}
	return syscall.Kill(-pgid, sig)
}

// processGroupAlive reports whether any live (non-zombie) process remains in
// pgid, and how many when that can be counted. On Linux /proc is scanned so
// zombies awaiting reaping by PID 1 do not count; elsewhere kill(-pgid, 0) is
// used (count -1 = unknown).
func processGroupAlive(pgid int) (bool, int) {
	if pgid <= 1 {
		return false, 0
	}
	if n, ok := liveGroupMembers(pgid); ok {
		return n > 0, n
	}
	err := syscall.Kill(-pgid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, -1
	}
	return false, 0
}
