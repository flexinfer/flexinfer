//go:build unix && !linux

package runtime

// liveGroupMembers has no /proc to scan off Linux; processGroupAlive falls back
// to kill(-pgid, 0).
func liveGroupMembers(int) (int, bool) { return 0, false }
