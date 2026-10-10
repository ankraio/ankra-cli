//go:build !windows

package cmd

import "syscall"

// execLocalTool replaces this process with the tool at path: the tool keeps
// the terminal, the signals and the exit code as if the shim were not there.
func execLocalTool(path string, argv []string, environment []string) error {
	return syscall.Exec(path, argv, environment) //nolint:gosec // the tool the user invoked, found on their PATH
}
