//go:build linux

package backends

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"idiom/internal/platform/failures"
)

// errProcUnavailable is the sentinel pidForPortViaProc returns when
// the kernel surface it needs (`/proc/net/tcp`, `/proc/<pid>/fd/`)
// is missing. Linux normally has both; the error covers exotic
// chroots / sandboxes that hide them.
var errProcUnavailable = failures.New(failures.Unavailable, reasonBackendProcUnavailable, "backends: /proc not available")

// pidForPortViaProc finds the PID listening on a TCP port by
// walking /proc/net/tcp{,6} for the LISTEN-state socket inode and
// then matching that inode against the symlinks in /proc/<pid>/fd/.
// It's the lsof fallback for minimal Linux containers; on every
// other platform the build picks up the stub that always errors.
//
// Layout of /proc/net/tcp lines (man 5 proc):
//
//	sl  local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
//	 0: 0100007F:BAB6 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 ...
//
// We only care about three fields: local_address (HEX:HEX), st
// (`0A` is LISTEN), and the inode (column 10).
func pidForPortViaProc(port int) (int, error) {
	inode, err := listenInodeForPort(port)
	if err != nil {
		return 0, err
	}
	if inode == 0 {
		return 0, nil
	}
	return pidForSocketInode(inode)
}

// listenInodeForPort scans both /proc/net/tcp (IPv4) and
// /proc/net/tcp6 (IPv6) for a LISTEN-state socket bound to port.
// Returns 0 (no error) when nothing matches; an error only when the
// /proc files themselves are unreachable.
func listenInodeForPort(port int) (uint64, error) {
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				return 0, errProcUnavailable
			}
			return 0, fmt.Errorf("backends: open %s: %w", path, err)
		}
		inode, scanErr := scanForListenInode(f, port)
		_ = f.Close()
		if scanErr != nil {
			return 0, scanErr
		}
		if inode != 0 {
			return inode, nil
		}
	}
	return 0, nil
}

func scanForListenInode(r interface {
	Read(p []byte) (n int, err error)
}, port int) (uint64, error) {
	scanner := bufio.NewScanner(r)
	scanner.Scan() // header line
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		// fields: 0:sl 1:local 2:remote 3:state 4:queues 5:tr 6:retrnsmt 7:uid 8:timeout 9:inode
		if fields[3] != "0A" {
			continue
		}
		colon := strings.IndexByte(fields[1], ':')
		if colon < 0 {
			continue
		}
		portHex := fields[1][colon+1:]
		p, err := strconv.ParseUint(portHex, 16, 32)
		if err != nil || int(p) != port {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}
		return inode, nil
	}
	return 0, scanner.Err()
}

// pidForSocketInode walks /proc/<pid>/fd/* looking for a symlink
// resolving to socket:[INODE]. Returns the first matching PID, or 0
// when no process owns that inode (which can happen briefly during
// a process exit). EACCES on a particular /proc/<pid>/fd/ entry is
// expected (we can't peek into other users' processes) and is
// silently skipped.
func pidForSocketInode(inode uint64) (int, error) {
	target := fmt.Sprintf("socket:[%d]", inode)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("backends: read /proc: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // EACCES on other users' processes; skip
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == target {
				return pid, nil
			}
		}
	}
	return 0, nil
}
