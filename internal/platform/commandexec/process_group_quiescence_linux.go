//go:build linux

package commandexec

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const managedProcessGroupSnapshotInterval = time.Millisecond

// waitManagedProcessGroupQuiescence keeps the direct leader unreaped while its
// process-group identity is reserved. It returns only after a /proc snapshot
// proves that no active nonleader process remains in that group. Zombies are
// already terminated and own no execution or resource handles, so a non-reaping
// parent cannot hold teardown open. There is deliberately no attempt or time
// ceiling.
func waitManagedProcessGroupQuiescence(pgid int, leaderPID int) error {
	if pgid <= 1 || leaderPID <= 1 {
		return fmt.Errorf(
			"observe managed process group quiescence: invalid group %d or leader %d",
			pgid,
			leaderPID,
		)
	}

	for {
		members, err := linuxProcessGroupMembers(pgid, leaderPID)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			return nil
		}

		time.Sleep(managedProcessGroupSnapshotInterval)
	}
}

func linuxProcessGroupMembers(pgid int, leaderPID int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("snapshot managed process group %d: %w", pgid, err)
	}

	members := make([]int, 0, 1)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == leaderPID {
			continue
		}

		process, err := linuxProcessStatForPID(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if process.processGroupID == pgid && linuxProcessStateActive(process.state) {
			members = append(members, pid)
		}
	}

	return members, nil
}

func linuxProcessStateActive(state byte) bool {
	switch state {
	case 'Z', 'X', 'x':
		return false
	default:
		return true
	}
}

type linuxProcessStat struct {
	processGroupID int
	state          byte
}

func linuxProcessStatForPID(pid int) (linuxProcessStat, error) {
	path := fmt.Sprintf("/proc/%d/stat", pid)
	body, err := os.ReadFile(path)
	if err != nil {
		return linuxProcessStat{}, fmt.Errorf("read process-group snapshot %s: %w", path, err)
	}

	// The comm field is parenthesized and may itself contain spaces or closing
	// parentheses. The final ')' is the only stable boundary before state, PPID,
	// and process-group ID.
	commEnd := strings.LastIndexByte(string(body), ')')
	if commEnd < 0 {
		return linuxProcessStat{}, fmt.Errorf("read process-group snapshot %s: malformed stat record", path)
	}
	fields := strings.Fields(string(body[commEnd+1:]))
	if len(fields) < 3 {
		return linuxProcessStat{}, fmt.Errorf("read process-group snapshot %s: incomplete stat record", path)
	}
	if len(fields[0]) != 1 {
		return linuxProcessStat{}, fmt.Errorf("read process-group snapshot %s: invalid process state", path)
	}

	pgid, err := strconv.Atoi(fields[2])
	if err != nil {
		return linuxProcessStat{}, fmt.Errorf("read process-group snapshot %s: parse group: %w", path, err)
	}

	return linuxProcessStat{processGroupID: pgid, state: fields[0][0]}, nil
}
