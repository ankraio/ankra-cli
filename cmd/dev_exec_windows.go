//go:build windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
)

// execLocalTool runs the tool at path as a child, since a Windows process
// cannot be replaced, and answers its exit code as a *devLocalExit.
func execLocalTool(path string, argv []string, environment []string) error {
	command := exec.Command(path, argv[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = environment
	runError := command.Run()
	var exitError *exec.ExitError
	if errors.As(runError, &exitError) {
		return &devLocalExit{code: exitError.ExitCode()}
	}
	return runError
}
