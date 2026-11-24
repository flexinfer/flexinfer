//go:build !linux && !darwin

package agent

import "io/fs"

// diskUsage falls back to the apparent size where lstat carries no block
// count; the agent ships for Linux only.
func diskUsage(info fs.FileInfo) (bytes int64, id fileID, multiLinked bool) {
	return info.Size(), fileID{}, false
}
