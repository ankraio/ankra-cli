//go:build windows

package cmd

// freeDiskBytes is not measured on Windows; the caller treats the space as
// unknown rather than as plentiful.
func freeDiskBytes(string) (int64, bool) {
	return 0, false
}
