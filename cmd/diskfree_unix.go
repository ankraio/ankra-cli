//go:build !windows

package cmd

import "syscall"

// freeDiskBytes reports how much the filesystem holding path can still take,
// or ok=false when it cannot tell.
func freeDiskBytes(path string) (int64, bool) {
	var stat syscall.Statfs_t
	if statError := syscall.Statfs(path, &stat); statError != nil {
		return 0, false
	}
	return int64(stat.Bavail) * int64(stat.Bsize), true
}
