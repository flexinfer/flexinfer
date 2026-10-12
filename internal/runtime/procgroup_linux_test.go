//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveGroupMembersSkipsZombiesAndOtherGroups(t *testing.T) {
	proc := t.TempDir()
	stats := map[string]string{
		"100": "100 (llama-server) S 7 100 1 0 -1",
		"101": "101 (python3 (engine) x) R 100 100 1 0 -1", // comm with spaces and ')'
		"102": "102 (runner) Z 100 100 1 0 -1",             // exited, awaiting reaping
		"103": "103 (other) S 7 103 1 0 -1",
		"104": "104 (dying) D 100 100 1 0 -1", // stuck in the driver: still holds VRAM
	}
	for pid, stat := range stats {
		require.NoError(t, os.MkdirAll(filepath.Join(proc, pid), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(proc, pid, "stat"), []byte(stat+"\n"), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(proc, "self"), 0o755))

	n, ok := liveGroupMembersIn(proc, 100)
	require.True(t, ok)
	assert.Equal(t, 3, n)

	n, ok = liveGroupMembersIn(proc, 999)
	require.True(t, ok)
	assert.Zero(t, n)

	_, ok = liveGroupMembersIn(filepath.Join(proc, "missing"), 100)
	assert.False(t, ok)
}

func TestParseProcStat(t *testing.T) {
	state, pgrp, ok := parseProcStat("4242 (a) b) c) S 1 4242 4242 0")
	require.True(t, ok)
	assert.Equal(t, "S", state)
	assert.Equal(t, 4242, pgrp)

	_, _, ok = parseProcStat("garbage")
	assert.False(t, ok)
}
