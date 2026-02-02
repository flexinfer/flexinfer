package secrets

import (
	"bytes"
	"os/exec"
)

// CommandExecutor is an interface for executing external commands.
// This allows mocking command execution in tests.
type CommandExecutor interface {
	// Run executes a command and returns stdout, stderr, and any error.
	Run(name string, args ...string) (stdout, stderr []byte, err error)
	// LookPath checks if a command is available in PATH.
	LookPath(file string) (string, error)
}

// RealCommandExecutor executes commands using os/exec.
type RealCommandExecutor struct{}

// Run executes a command and returns its output.
func (r *RealCommandExecutor) Run(name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command(name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// LookPath checks if a command is available in PATH.
func (r *RealCommandExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// defaultExecutor is the global command executor.
// In tests, this can be replaced with a mock.
var defaultExecutor CommandExecutor = &RealCommandExecutor{}

// SetExecutor sets the command executor for testing.
// Returns the previous executor for restoration.
func SetExecutor(e CommandExecutor) CommandExecutor {
	prev := defaultExecutor
	defaultExecutor = e
	return prev
}
