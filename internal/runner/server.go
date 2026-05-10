package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Server is the local job-queue daemon. One instance per machine
// (per Unix socket); the cap on concurrency is per-pool, not
// per-server, so multiple projects can share the same daemon and
// each picks the pool that maps to its constraint (e.g. one pool per
// MLX backend).
type Server struct {
	cfg        Config
	socketPath string
	startedAt  time.Time

	mu    sync.Mutex
	pools map[string]*pool
	jobs  map[string]*job

	idCounter atomic.Uint64
	listener  net.Listener
	closed    chan struct{}
	wg        sync.WaitGroup
}

// pool tracks one named concurrency bucket. Slot accounting is
// "in-flight count vs max"; the in-flight slice exists for status
// reporting only — we never index into it for grant decisions.
type pool struct {
	name        string
	maxInFlight int
	inFlight    []*job
	waiting     []*job
}

// job is the runner-internal view of a queued or running task. The
// connection handler that owns the job watches grantedCh + posCh; the
// dispatcher (anyone holding Server.mu) closes grantedCh on promotion
// and pushes onto posCh on shifts.
//
// posCh is buffered so a slow consumer doesn't block the dispatcher;
// if the buffer fills we drop position updates rather than block — a
// missed update is harmless (the next shift will refresh).
type job struct {
	ID          string
	Pool        string
	Label       string
	Client      string
	SubmittedAt time.Time
	StartedAt   time.Time

	grantedCh chan struct{}
	posCh     chan int
	grantOnce sync.Once
}

// NewServer constructs a server with the given config and socket
// path. It does not bind the socket — call Start for that. Splitting
// construction from binding lets tests use ServeListener with an
// arbitrary net.Listener without touching the filesystem.
func NewServer(cfg Config, socketPath string) *Server {
	pools := make(map[string]*pool, len(cfg.Pools))
	for name, pc := range cfg.Pools {
		pools[name] = &pool{name: name, maxInFlight: pc.MaxInFlight}
	}
	return &Server{
		cfg:        cfg,
		socketPath: socketPath,
		startedAt:  time.Now(),
		pools:      pools,
		jobs:       make(map[string]*job),
		closed:     make(chan struct{}),
	}
}

// Start binds the Unix socket and begins accepting connections in a
// background goroutine. Returns immediately once the socket is bound;
// the caller should defer Stop. If a stale socket file exists at the
// path we attempt to remove it — Unix sockets don't auto-cleanup
// after a crash and a leftover file would block bind.
func (s *Server) Start() error {
	if s.socketPath == "" {
		return errors.New("runner: empty socket path")
	}
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("runner: create socket dir: %w", err)
	}
	// Best-effort cleanup of a leftover socket file from a prior run.
	// Unix sockets are filesystem entries; the kernel doesn't remove
	// them when the listening process dies, so a crashed daemon would
	// otherwise refuse to restart with EADDRINUSE. We only unlink if
	// the path is a socket — protects against accidentally deleting
	// a regular file the operator put at this path.
	if fi, err := os.Stat(s.socketPath); err == nil && fi.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(s.socketPath)
	}
	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("runner: listen %q: %w", s.socketPath, err)
	}
	// Tighten permissions: this is per-user state, not shared.
	_ = os.Chmod(s.socketPath, 0o600)
	s.listener = ln
	s.wg.Add(1)
	go s.serve()
	return nil
}

// ServeListener is the testable variant of Start. Tests pass an
// in-process net.Listener (e.g. via net.Pipe or a temp socket) and
// the server uses it directly. Production code uses Start.
func (s *Server) ServeListener(ln net.Listener) {
	s.listener = ln
	s.wg.Add(1)
	go s.serve()
}

// Stop closes the listener and waits for in-flight handlers to
// drain. Holders of slots see their connections closed; from their
// perspective the daemon disappeared and they should retry. Stop is
// idempotent — calling twice is a no-op.
func (s *Server) Stop() {
	select {
	case <-s.closed:
		return
	default:
	}
	close(s.closed)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()
	if s.socketPath != "" {
		// Best-effort: leftover socket file would block the next start.
		_ = os.Remove(s.socketPath)
	}
}

// SocketPath reports the path the server is bound to. Mostly useful
// for tests that bind to a temp directory and want to hand the path
// to a client.
func (s *Server) SocketPath() string { return s.socketPath }

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			// Transient accept errors (e.g. EMFILE under load) are
			// rare on a Unix socket; log-and-continue keeps the
			// daemon alive rather than tearing down on first hiccup.
			continue
		}
		s.wg.Add(1)
		go s.handle(conn)
	}
}

// firstRequestTimeout caps how long the server will wait for a
// connecting client to send its action. Without this a client that
// connects and never writes pins a goroutine forever — a small but
// real connection-leak vector. Acquire keeps the conn open AFTER the
// first read; the deadline is cleared on the success path below.
const firstRequestTimeout = 5 * time.Second

func (s *Server) handle(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// One request per connection. Read the first line, decode, dispatch.
	// For acquire we keep the connection open and stream updates; for
	// other actions we write one response and close.
	_ = conn.SetReadDeadline(time.Now().Add(firstRequestTimeout))
	dec := json.NewDecoder(conn)
	var req Request
	if err := dec.Decode(&req); err != nil {
		writeResponse(conn, Response{Error: fmt.Sprintf("decode request: %v", err)})
		return
	}
	// Clear the deadline now that we've read the action — Acquire
	// connections legitimately stay open indefinitely.
	_ = conn.SetReadDeadline(time.Time{})
	switch req.Action {
	case ActionAcquire:
		s.handleAcquire(conn, req)
	case ActionStatus:
		s.handleStatus(conn)
	case ActionPing:
		s.handlePing(conn)
	case ActionCancel:
		s.handleCancel(conn, req)
	default:
		writeResponse(conn, Response{Error: fmt.Sprintf("unknown action %q", req.Action)})
	}
}

func (s *Server) handleAcquire(conn net.Conn, req Request) {
	pool := req.Pool
	if pool == "" {
		pool = "default"
	}
	j, position, err := s.enqueue(pool, req.Label, req.Client)
	if err != nil {
		writeResponse(conn, Response{Error: err.Error()})
		return
	}
	// Always release on exit, whether granted-then-disconnect or
	// disconnect-while-waiting. The release path is idempotent so
	// duplicate calls are safe.
	defer s.release(j.ID)

	// First frame: position (or implicit grant if position == 0; the
	// dispatcher closed grantedCh in that case, which the loop below
	// will pick up immediately).
	if err := writeResponse(conn, Response{JobID: j.ID, Pool: j.Pool, Position: position}); err != nil {
		return
	}

	// Watch the connection for client-side close. We don't expect
	// further data — the protocol holds the slot via the open
	// connection — but a Read of zero bytes returns when the peer
	// closes, which is exactly the signal we want.
	disconnect := make(chan struct{})
	go func() {
		defer close(disconnect)
		_, _ = io.Copy(io.Discard, conn)
	}()

	for {
		select {
		case <-j.grantedCh:
			if err := writeResponse(conn, Response{JobID: j.ID, Pool: j.Pool, Granted: true}); err != nil {
				return
			}
			// Hold the slot until the client disconnects.
			select {
			case <-disconnect:
			case <-s.closed:
			}
			return
		case pos := <-j.posCh:
			if err := writeResponse(conn, Response{JobID: j.ID, Pool: j.Pool, Position: pos}); err != nil {
				return
			}
		case <-disconnect:
			return
		case <-s.closed:
			return
		}
	}
}

func (s *Server) handleStatus(conn net.Conn) {
	snap := s.snapshot()
	_ = writeResponse(conn, Response{Status: &snap})
}

func (s *Server) handlePing(conn net.Conn) {
	_ = writeResponse(conn, Response{
		Pong:   true,
		Uptime: time.Since(s.startedAt).Seconds(),
	})
}

func (s *Server) handleCancel(conn net.Conn, req Request) {
	if req.JobID == "" {
		_ = writeResponse(conn, Response{Error: "cancel: job_id required"})
		return
	}
	ok, errMsg := s.cancel(req.JobID)
	if !ok {
		_ = writeResponse(conn, Response{Error: errMsg})
		return
	}
	_ = writeResponse(conn, Response{JobID: req.JobID})
}

// enqueue inserts a job into a pool. If a slot is free it grants
// immediately (position 0, grantedCh pre-closed); otherwise it
// returns the 1-indexed waiting position. Either way the job is
// tracked in s.jobs until release.
func (s *Server) enqueue(poolName, label, client string) (*job, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pools[poolName]
	if !ok {
		// List known pools in the error so misconfigured callers can
		// see the surface without grepping.
		known := make([]string, 0, len(s.pools))
		for n := range s.pools {
			known = append(known, n)
		}
		sort.Strings(known)
		return nil, 0, fmt.Errorf("unknown pool %q (known: %v)", poolName, known)
	}
	id := s.newJobID()
	j := &job{
		ID:          id,
		Pool:        poolName,
		Label:       label,
		Client:      client,
		SubmittedAt: time.Now(),
		grantedCh:   make(chan struct{}),
		// Buffered: dispatcher pushes non-blocking; missed updates
		// are harmless because the next shift refreshes everyone.
		posCh: make(chan int, 4),
	}
	s.jobs[id] = j
	if len(p.inFlight) < p.maxInFlight {
		j.StartedAt = time.Now()
		p.inFlight = append(p.inFlight, j)
		j.grantOnce.Do(func() { close(j.grantedCh) })
		return j, 0, nil
	}
	p.waiting = append(p.waiting, j)
	return j, len(p.waiting), nil
}

// release removes a job by ID. If it was in-flight, the head of the
// waiting list is promoted and everyone behind shifts; if it was
// waiting, everyone behind shifts. Idempotent — releasing an unknown
// or already-released job is a no-op.
func (s *Server) release(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[jobID]
	if !ok {
		return
	}
	delete(s.jobs, jobID)
	p, ok := s.pools[j.Pool]
	if !ok {
		return
	}
	// In-flight path: free a slot, promote head of waiting.
	for i, inflight := range p.inFlight {
		if inflight.ID != jobID {
			continue
		}
		p.inFlight = append(p.inFlight[:i], p.inFlight[i+1:]...)
		if len(p.waiting) > 0 {
			next := p.waiting[0]
			p.waiting = p.waiting[1:]
			next.StartedAt = time.Now()
			p.inFlight = append(p.inFlight, next)
			next.grantOnce.Do(func() { close(next.grantedCh) })
		}
		s.broadcastPositions(p)
		return
	}
	// Waiting path: drop from queue, shift everyone behind.
	for i, waiting := range p.waiting {
		if waiting.ID != jobID {
			continue
		}
		p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
		s.broadcastPositions(p)
		return
	}
}

// cancel removes a waiting job by ID. In-flight cancellations are
// rejected — the daemon doesn't own the worker process and shouldn't
// pretend to kill it; the operator should ctrl-C the wrapping
// command instead. Returns ok=false with a human-readable reason on
// failure.
func (s *Server) cancel(jobID string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[jobID]
	if !ok {
		return false, fmt.Sprintf("unknown job_id %q", jobID)
	}
	p, ok := s.pools[j.Pool]
	if !ok {
		return false, fmt.Sprintf("unknown pool %q for job %q", j.Pool, jobID)
	}
	for _, inflight := range p.inFlight {
		if inflight.ID == jobID {
			return false, fmt.Sprintf("job %q is in-flight; ctrl-C the holding command instead", jobID)
		}
	}
	for i, waiting := range p.waiting {
		if waiting.ID != jobID {
			continue
		}
		p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
		delete(s.jobs, jobID)
		s.broadcastPositions(p)
		return true, ""
	}
	return false, fmt.Sprintf("job %q not found in pool %q", jobID, j.Pool)
}

// broadcastPositions pushes new 1-indexed positions to every waiter
// in p. Non-blocking: if a waiter's posCh is full we drop, since the
// next shift will deliver a fresher value. Caller MUST hold s.mu.
func (s *Server) broadcastPositions(p *pool) {
	for i, w := range p.waiting {
		select {
		case w.posCh <- i + 1:
		default:
		}
	}
}

// Snapshot returns the public-facing status payload. Callers (the
// CLI's `serve` boot message, tests) use this to render pool state
// without going through a socket round trip.
func (s *Server) Snapshot() StatusPayload { return s.snapshot() }

func (s *Server) snapshot() StatusPayload {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.pools))
	for name := range s.pools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := StatusPayload{
		Pools:    make([]PoolStatus, 0, len(names)),
		InFlight: []JobStatus{},
		Waiting:  []JobStatus{},
	}
	for _, name := range names {
		p := s.pools[name]
		out.Pools = append(out.Pools, PoolStatus{
			Name:        name,
			MaxInFlight: p.maxInFlight,
			InFlight:    len(p.inFlight),
			Waiting:     len(p.waiting),
		})
		for _, j := range p.inFlight {
			out.InFlight = append(out.InFlight, JobStatus{
				ID:          j.ID,
				Pool:        j.Pool,
				Label:       j.Label,
				Client:      j.Client,
				SubmittedAt: FormatTime(j.SubmittedAt),
				StartedAt:   FormatTime(j.StartedAt),
			})
		}
		for i, j := range p.waiting {
			out.Waiting = append(out.Waiting, JobStatus{
				ID:          j.ID,
				Pool:        j.Pool,
				Label:       j.Label,
				Client:      j.Client,
				SubmittedAt: FormatTime(j.SubmittedAt),
				Position:    i + 1,
			})
		}
	}
	return out
}

func (s *Server) newJobID() string {
	n := s.idCounter.Add(1)
	// j-<unix-ms>-<seq>: monotonic and human-readable; the server
	// recycles IDs after restart but each shift's IDs are unique.
	return fmt.Sprintf("j-%d-%d", time.Now().UnixMilli(), n)
}

// writeResponse marshals and writes one JSON line. Newline-terminated
// so a streaming reader on the other end can json.Decode in a loop.
func writeResponse(w io.Writer, r Response) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// Run is a small convenience for `idiom queue serve` style binaries:
// start the server, wait until ctx is canceled (typically by SIGINT),
// then stop. Tests use Start/Stop directly to avoid signal coupling.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	s.Stop()
	return nil
}

