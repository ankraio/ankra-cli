//go:build windows

package affected

import "os/exec"

// killProcessGroupOnCancel does nothing on Windows, where Analyse refuses to
// run before it starts a process.
func killProcessGroupOnCancel(command *exec.Cmd) {}
