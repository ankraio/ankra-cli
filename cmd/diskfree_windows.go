//go:build windows

package cmd

// statfsFreeBytes is not measured on Windows; the caller treats the space as
// unknown rather than as plentiful.
func statfsFreeBytes(string) (int64, bool) {
	return 0, false
}
