//go:build linux || darwin

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// du counts a file with several hard links once.
func TestLocalCacheMeasure_HardLinksCountedOnce(t *testing.T) {
	dir := t.TempDir()
	writeSized(t, filepath.Join(dir, "blob"), 1000)
	require.NoError(t, os.Link(filepath.Join(dir, "blob"), filepath.Join(dir, "snapshot-link")))

	inv := newLocalCacheInventory(dir, "node-du", &fakeModelKeys{}, logr.Discard())
	inv.usage = apparentUsage
	got, err := inv.measure(dir)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), got)
}

// The production measure is allocated blocks, so a written file is never
// counted as empty.
func TestDiskUsage_AllocatedBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w")
	writeSized(t, path, 64<<10)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	bytes, _, multi := diskUsage(info)
	assert.Positive(t, bytes)
	assert.Zero(t, bytes%512, "a multiple of the 512-byte stat block")
	assert.False(t, multi)
}
