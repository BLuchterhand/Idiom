package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// ErrDaemonUnreachable wraps the underlying dial error so the CLI
// layer can recognize "daemon isn't running" cleanly and print the
// canonical "start it with `idiom queue serve`" hint.
var ErrDaemonUnreachable = errors.New("runner daemon unreachable")

// dialTimeout is short by design — if the daemon isn't already
// listening we want to fail fast and tell the user how to start it,
// not stall a CLI invocation for seconds.
const dialTimeout = 2 * time.Second

// Status fetches a single snapshot from the daemon. One round trip,
// connection closes after the reply. Returned StatusPayload is safe
// to print directly; pools/jobs are pre-sorted by the server.
func roundTrip(socketPath string, req Request) (Response, error) {
	conn, err := dial(socketPath)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := writeRequest(conn, req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("decode %s: %w", req.Action, err)
	}
	if resp.Error != "" {
		return Response{}, errors.New(resp.Error)
	}
	return resp, nil
}

func Status(socketPath string) (StatusPayload, error) {
	resp, err := roundTrip(socketPath, Request{Action: ActionStatus})
	if err != nil {
		return StatusPayload{}, err
	}
	if resp.Status == nil {
		return StatusPayload{}, errors.New("status response missing payload")
	}
	return *resp.Status, nil
}

// Ping reports whether the daemon is responsive and how long it has
// been up. Useful for `idiom queue ping` and as a self-test from
// install scripts that need to know whether to start the daemon.
func Ping(socketPath string) (uptime time.Duration, err error) {
	resp, err := roundTrip(socketPath, Request{Action: ActionPing})
	if err != nil {
		return 0, err
	}
	return time.Duration(resp.Uptime * float64(time.Second)), nil
}

// Cancel removes a waiting job by ID. Returns an error if the job is
// in-flight or unknown — the daemon refuses to cancel running jobs
// (the operator should ctrl-C the holding command instead).
func Cancel(socketPath, jobID string) error {
	_, err := roundTrip(socketPath, Request{Action: ActionCancel, JobID: jobID})
	return err
}

// Lease holds an acquired slot. The caller's job runs while the
// lease is open; calling Close (or letting the lease's context end)
// releases the slot. Pre-grant frames (position updates) are
// delivered on the Updates channel so callers can show progress.
type Lease struct {
	JobID string
	Pool  string

	conn      net.Conn
	updates   chan int
	granted   chan struct{}
	errCh     chan error
	closed    chan struct{}
	closeOnce sync.Once
}

// Updates returns a channel of position updates emitted while the
// lease is still in the queue. The channel is closed when the slot
// is granted (or the lease is canceled). Range over this in a
// goroutine to surface progress to the user.
func (l *Lease) Updates() <-chan int { return l.updates }

// Granted is closed when the daemon promotes this lease to in-flight.
// After Granted fires the caller may run their work; Close releases
// the slot when done.
func (l *Lease) Granted() <-chan struct{} { return l.granted }

// Err returns any background error encountered while reading the
// daemon's stream (e.g. daemon shut down mid-wait). The channel is
// buffered, so a one-shot Recv is safe; receiving nil means clean.
func (l *Lease) Err() <-chan error { return l.errCh }

// Close releases the slot. Idempotent. Always defer this after a
// successful Acquire — the slot stays held until the connection
// closes, even if the caller has already finished its work.
func (l *Lease) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.closed)
		err = l.conn.Close()
	})
	return err
}

// Acquire opens a connection, sends an acquire request, and starts a
// background reader that fans daemon frames into Updates / Granted /
// Err. The first frame (initial position) is read synchronously so
// the caller sees a quick error if the pool is unknown or the daemon
// is misbehaving; everything after is async.
//
// The caller is responsible for calling Lease.Close exactly once.
func Acquire(ctx context.Context, socketPath, pool, label, client string) (*Lease, error) {
	if pool == "" {
		pool = "default"
	}
	conn, err := dial(socketPath)
	if err != nil {
		return nil, err
	}
	if err := writeRequest(conn, Request{
		Action: ActionAcquire,
		Pool:   pool,
		Label:  label,
		Client: client,
	}); err != nil {
		conn.Close()
		return nil, err
	}
	dec := json.NewDecoder(conn)
	var first Response
	if err := dec.Decode(&first); err != nil {
		conn.Close()
		return nil, fmt.Errorf("decode acquire reply: %w", err)
	}
	if first.Error != "" {
		conn.Close()
		return nil, errors.New(first.Error)
	}
	lease := &Lease{
		JobID:   first.JobID,
		Pool:    first.Pool,
		conn:    conn,
		updates: make(chan int, 8),
		granted: make(chan struct{}),
		errCh:   make(chan error, 1),
		closed:  make(chan struct{}),
	}
	// If the very first frame already carries Granted, fast-path it.
	// Otherwise the first frame is an initial position; surface it.
	if first.Granted {
		close(lease.granted)
	} else if first.Position > 0 {
		lease.updates <- first.Position
	}

	go lease.readLoop(dec)
	go func() {
		// If the caller's context is canceled before we're granted,
		// closing the lease tears down the connection and tells the
		// daemon (via EOF) to drop us from the queue.
		select {
		case <-ctx.Done():
			_ = lease.Close()
		case <-lease.closed:
		}
	}()
	return lease, nil
}

func (l *Lease) readLoop(dec *json.Decoder) {
	defer close(l.updates)
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			select {
			case <-l.closed:
				// Caller closed; not an error.
			default:
				select {
				case l.errCh <- err:
				default:
				}
			}
			return
		}
		if resp.Error != "" {
			select {
			case l.errCh <- errors.New(resp.Error):
			default:
			}
			return
		}
		if resp.Granted {
			select {
			case <-l.granted:
			default:
				close(l.granted)
			}
			// Daemon won't send more frames after Granted; the
			// connection just stays open until Close. Returning here
			// is fine — readLoop is done.
			return
		}
		if resp.Position > 0 {
			select {
			case l.updates <- resp.Position:
			default:
				// Drop on full buffer; next update refreshes.
			}
		}
	}
}

func dial(socketPath string) (net.Conn, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("%w: empty socket path", ErrDaemonUnreachable)
	}
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}
	return conn, nil
}

func writeRequest(w io.Writer, req Request) error {
	return writeJSON(w, req)
}
