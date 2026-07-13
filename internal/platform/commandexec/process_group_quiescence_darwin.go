//go:build darwin

package commandexec

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

const managedProcessGroupSnapshotInterval = time.Millisecond

const darwinProcessStateZombie = 5

// waitManagedProcessGroupQuiescence keeps the direct leader unreaped while its
// process-group identity is reserved. It returns only after kern.proc.pgrp
// proves that no active nonleader process remains. Zombies are already
// terminated and own no execution or resource handles, so a non-reaping parent
// cannot hold teardown open. There is deliberately no attempt or time ceiling.
func waitManagedProcessGroupQuiescence(pgid int, leaderPID int) error {
	if pgid <= 1 || leaderPID <= 1 {
		return fmt.Errorf(
			"observe managed process group quiescence: invalid group %d or leader %d",
			pgid,
			leaderPID,
		)
	}

	for {
		processes, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
		if err != nil {
			return fmt.Errorf("snapshot managed process group %d: %w", pgid, err)
		}

		hasNonleader := false
		for _, process := range processes {
			pid := int(process.Proc.P_pid)
			if pid > 1 && pid != leaderPID && process.Proc.P_stat != darwinProcessStateZombie {
				hasNonleader = true
				break
			}
		}
		if !hasNonleader {
			return nil
		}

		time.Sleep(managedProcessGroupSnapshotInterval)
	}
}
