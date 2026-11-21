//go:build linux || darwin

package agent

import (
	"io/fs"
	"syscall"
)

// diskUsage is du's measure of one lstat result: allocated 512-byte blocks,
// plus the inode identity so a file with several hard links is counted once.
func diskUsage(info fs.FileInfo) (bytes int64, id fileID, multiLinked bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size(), fileID{}, false
	}
	return int64(st.Blocks) * 512, fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, !info.IsDir() && st.Nlink > 1
}
