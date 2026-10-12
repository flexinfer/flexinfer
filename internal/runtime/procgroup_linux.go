//go:build linux

package runtime

import (
	"os"
	"strconv"
	"strings"
)

// liveGroupMembers counts non-zombie processes whose process group is pgid by
// scanning /proc/<pid>/stat. Zombies are excluded: an orphaned grandchild that
// already exited stays visible to kill(-pgid, 0) until PID 1 reaps it, but it
// no longer holds GPU memory.
func liveGroupMembers(pgid int) (int, bool) {
	return liveGroupMembersIn("/proc", pgid)
}

func liveGroupMembersIn(procRoot string, pgid int) (int, bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, false
	}
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(procRoot + "/" + e.Name() + "/stat")
		if err != nil {
			continue // exited between ReadDir and ReadFile
		}
		state, pgrp, ok := parseProcStat(string(data))
		if ok && pgrp == pgid && state != "Z" && state != "X" {
			n++
		}
	}
	return n, true
}

// parseProcStat extracts the state and process group from a /proc/<pid>/stat
// line ("pid (comm) state ppid pgrp ..."). comm may contain spaces or ')', so
// fields are taken after the last ')'.
func parseProcStat(stat string) (state string, pgrp int, ok bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", 0, false
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 3 {
		return "", 0, false
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, false
	}
	return fields[0], pgrp, true
}
