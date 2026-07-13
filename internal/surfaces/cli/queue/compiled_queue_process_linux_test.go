//go:build linux

package queue

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func observeCompiledQueueProcessExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}

		return err
	}
}

func compiledQueueProcessSnapshot() ([]compiledQueueProcessIdentity, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}

	identities := make([]compiledQueueProcessIdentity, 0, len(entries))
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 1 {
			continue
		}
		identity, identityErr := compiledQueueProcessIdentityForPID(pid)
		if errors.Is(identityErr, os.ErrNotExist) {
			continue
		}
		if identityErr != nil {
			continue
		}
		identities = append(identities, identity)
	}

	return identities, nil
}

func compiledQueueProcessIdentityForPID(pid int) (compiledQueueProcessIdentity, error) {
	body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return compiledQueueProcessIdentity{}, os.ErrNotExist
		}

		return compiledQueueProcessIdentity{}, fmt.Errorf("read process %d identity: %w", pid, err)
	}

	stat := string(body)
	closeName := strings.LastIndex(stat, ")")
	if closeName < 0 {
		return compiledQueueProcessIdentity{}, fmt.Errorf("read process %d identity: malformed stat", pid)
	}
	fields := strings.Fields(stat[closeName+1:])
	if len(fields) < 20 {
		return compiledQueueProcessIdentity{}, fmt.Errorf("read process %d identity: short stat", pid)
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return compiledQueueProcessIdentity{}, fmt.Errorf("read process %d parent: %w", pid, err)
	}
	groupID, err := strconv.Atoi(fields[2])
	if err != nil {
		return compiledQueueProcessIdentity{}, fmt.Errorf("read process %d group: %w", pid, err)
	}

	return compiledQueueProcessIdentity{
		PID:        pid,
		ParentPID:  parentPID,
		GroupID:    groupID,
		StartToken: fields[19],
	}, nil
}
