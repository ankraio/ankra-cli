//go:build !darwin && !linux

package cmd

import "os"

// onboardQueryBackground does not ask the terminal here; the white logo, made
// for the dark backgrounds most terminals default to, is used.
func onboardQueryBackground(_, _ *os.File) []byte { return nil }
