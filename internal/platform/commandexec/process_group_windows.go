//go:build windows

package commandexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureDetachedProcessGroup(cmd *exec.Cmd) error {
	return errors.New("detached process group is not supported on windows")
}

// managedProcessTree owns the Windows handles needed to observe and terminate
// a process tree independently of exec.Cmd's direct-process handle. The child
// starts suspended, enters the Job Object, and only then resumes, so it cannot
// create a descendant outside commandexec's ownership boundary.
type managedProcessTree struct {
	cmd *exec.Cmd
	job windows.Handle

	attachOnce sync.Once
	attachDone chan struct{}
	attachErr  error

	handlesMu sync.RWMutex
	process   windows.Handle
	attached  bool

	terminateOnce                 sync.Once
	terminateErr                  error
	terminateJobObject            func(windows.Handle) error
	closeJobOnTerminationFailure  func(windows.Handle) error
	jobTerminationFallback        jobTerminationFallbackState
	waitForDirectProcessExitEvent func(windows.Handle, uint32) (uint32, error)

	teardownDone chan struct{}
	teardownMu   sync.Mutex
	teardownErr  error
	quiesceOnce  sync.Once
	quiesceErr   error
	jobProcesses map[uint32]jobProcessObservation

	closeOnce sync.Once
	closeErr  error
}

type jobTerminationFallbackState uint8

const (
	jobTerminationFallbackNone jobTerminationFallbackState = iota
	jobTerminationFallbackClosed
	jobTerminationFallbackCloseFailed
)

func newManagedProcessTree(cmd *exec.Cmd, _ int) (*managedProcessTree, error) {
	if cmd == nil {
		return nil, errors.New("managed process command is required")
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create managed process job: %w", err)
	}

	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		configureErr := fmt.Errorf("configure managed process job: %w", err)
		if closeErr := windows.CloseHandle(job); closeErr != nil {
			return nil, errors.Join(configureErr, fmt.Errorf("close unconfigured process job: %w", closeErr))
		}

		return nil, configureErr
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED

	return &managedProcessTree{
		cmd:                           cmd,
		job:                           job,
		attachDone:                    make(chan struct{}),
		terminateJobObject:            terminateJob,
		closeJobOnTerminationFailure:  windows.CloseHandle,
		waitForDirectProcessExitEvent: windows.WaitForSingleObject,
		teardownDone:                  make(chan struct{}),
	}, nil
}

// attach assigns the suspended direct process to the Job Object and resumes
// its initial thread. terminate waits for this transaction to finish, so
// cancellation cannot race a direct-process fallback against Job assignment.
func (tree *managedProcessTree) attach() error {
	if tree == nil {
		return nil
	}

	tree.attachOnce.Do(func() {
		tree.attachErr = tree.attachSuspendedProcess()
		close(tree.attachDone)
		if tree.attachErr != nil {
			terminateErr := tree.terminate()
			quiescenceErr := tree.waitForJobQuiescence()
			tree.teardownMu.Lock()
			tree.teardownErr = errors.Join(
				normalizeManagedTerminationError(terminateErr),
				quiescenceErr,
			)
			tree.teardownMu.Unlock()
			close(tree.teardownDone)
			return
		}

		go tree.observeExit()
	})

	return tree.attachErr
}

func (tree *managedProcessTree) attachSuspendedProcess() error {
	if tree.cmd == nil || tree.cmd.Process == nil || tree.cmd.Process.Pid <= 0 {
		return errors.New("managed process has no started direct process")
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|
			windows.PROCESS_TERMINATE|
			windows.PROCESS_QUERY_LIMITED_INFORMATION|
			windows.SYNCHRONIZE,
		false,
		uint32(tree.cmd.Process.Pid),
	)
	if err != nil {
		attachErr := fmt.Errorf("open managed direct process: %w", err)
		return errors.Join(attachErr, tree.killUnattachedDirectProcess())
	}

	tree.handlesMu.Lock()
	tree.process = process
	tree.handlesMu.Unlock()

	if err := windows.AssignProcessToJobObject(tree.job, process); err != nil {
		attachErr := fmt.Errorf("assign direct process to managed job: %w", err)
		return errors.Join(attachErr, tree.killUnattachedDirectProcess())
	}

	tree.handlesMu.Lock()
	tree.attached = true
	tree.handlesMu.Unlock()

	if err := resumeSuspendedProcess(uint32(tree.cmd.Process.Pid)); err != nil {
		resumeErr := fmt.Errorf("resume managed direct process: %w", err)
		return errors.Join(resumeErr, tree.terminateAttachedJob())
	}

	return nil
}

// abortAttach releases cancellation waiters when exec.Cmd.Start fails before a
// process exists. Closing the Job Object remains close's responsibility.
func (tree *managedProcessTree) abortAttach() error {
	if tree == nil {
		return nil
	}

	tree.attachOnce.Do(func() {
		tree.attachErr = errors.New("managed process did not start")
		close(tree.attachDone)
		close(tree.teardownDone)
	})

	return tree.attachErr
}

func (tree *managedProcessTree) observeExit() {
	observeErr := tree.observeDirectProcessExit()
	terminateErr := tree.terminate()
	quiescenceErr := tree.waitForJobQuiescence()

	tree.teardownMu.Lock()
	tree.teardownErr = errors.Join(
		observeErr,
		normalizeManagedTerminationError(terminateErr),
		quiescenceErr,
	)
	tree.teardownMu.Unlock()
	close(tree.teardownDone)
}

type jobProcessObservation struct {
	handle windows.Handle
	owned  bool
}

type jobObjectBasicProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIDsInList  uint32
	ProcessIDList             [1]uintptr
}

var isProcessInJobProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// waitForJobQuiescence returns only after every process identity captured from
// the Job Object has a signaled process handle. Unlike ActiveProcesses, process
// handle signaling does not depend on releasing commandexec's retained handles.
func (tree *managedProcessTree) waitForJobQuiescence() error {
	if tree == nil {
		return nil
	}

	tree.quiesceOnce.Do(func() {
		tree.quiesceErr = tree.waitForJobQuiescenceOnce()
	})

	return tree.quiesceErr
}

func (tree *managedProcessTree) waitForJobQuiescenceOnce() error {
	observations := tree.jobProcesses
	if observations == nil {
		observations = make(map[uint32]jobProcessObservation)
	}
	tree.handlesMu.RLock()
	job := tree.job
	fallback := tree.jobTerminationFallback
	if tree.process != 0 && tree.cmd != nil && tree.cmd.Process != nil && tree.cmd.Process.Pid > 0 {
		pid := uint32(tree.cmd.Process.Pid)
		if _, exists := observations[pid]; !exists {
			observations[pid] = jobProcessObservation{handle: tree.process}
		}
	}
	tree.handlesMu.RUnlock()

	finish := func(err error) error {
		closeErr := closeJobProcessObservations(observations)
		tree.jobProcesses = nil
		return errors.Join(err, closeErr)
	}

	switch fallback {
	case jobTerminationFallbackClosed:
		if job != 0 {
			return finish(errors.New("observe managed process job quiescence: closed fallback retained a job handle"))
		}

		return finish(waitForJobProcessObservations(observations))
	case jobTerminationFallbackCloseFailed:
		// The direct process was force-killed by terminate, but descendants
		// may still be live because neither Job operation succeeded. Waiting
		// on their handles here would hide the terminal failure forever. The
		// owning close still retains and closes the Job handle after cmd.Wait.
		return finish(nil)
	case jobTerminationFallbackNone:
		if job == 0 {
			return finish(errors.New("observe managed process job quiescence: job handle is unavailable"))
		}
	default:
		return finish(fmt.Errorf("observe managed process job quiescence: unknown fallback state %d", fallback))
	}

	for {
		var added bool
		var err error
		observations, added, err = tree.captureJobProcessObservations(observations)
		if err != nil {
			return finish(err)
		}
		if added {
			if err := terminateJob(tree.job); err != nil {
				return finish(err)
			}
		}
		if err := waitForJobProcessObservations(observations); err != nil {
			return finish(err)
		}

		observations, added, err = tree.captureJobProcessObservations(observations)
		if err != nil {
			return finish(err)
		}
		if !added {
			return finish(nil)
		}

		// A process that joined during termination is still signaled only
		// through the Job Object. The next loop waits its pinned handle.
		if err := terminateJob(tree.job); err != nil {
			return finish(err)
		}
	}
}

func (tree *managedProcessTree) captureJobProcessObservations(
	observations map[uint32]jobProcessObservation,
) (map[uint32]jobProcessObservation, bool, error) {
	if observations == nil {
		observations = make(map[uint32]jobProcessObservation)
	}

	processIDs, err := queryJobProcessIDs(tree.job)
	if err != nil {
		return observations, false, err
	}

	added := false
	for _, processID := range processIDs {
		if processID == 0 {
			continue
		}
		if _, exists := observations[processID]; exists {
			continue
		}

		process, err := windows.OpenProcess(
			windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
			false,
			processID,
		)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			// The snapshotted process exited before its identity could be
			// pinned. It is already quiescent.
			continue
		}
		if err != nil {
			return observations, added, fmt.Errorf("open managed job process %d: %w", processID, err)
		}

		inJob, membershipErr := processBelongsToJob(process, tree.job)
		if membershipErr != nil {
			return observations, added, errors.Join(
				membershipErr,
				closeWindowsHandle(process, fmt.Sprintf("managed job process %d observation", processID)),
			)
		}
		if !inJob {
			if err := closeWindowsHandle(process, fmt.Sprintf("reused process %d observation", processID)); err != nil {
				return observations, added, err
			}
			continue
		}

		observations[processID] = jobProcessObservation{handle: process, owned: true}
		added = true
	}

	return observations, added, nil
}

func queryJobProcessIDs(job windows.Handle) ([]uint32, error) {
	if job == 0 {
		return nil, errors.New("query managed job process identities: job handle is unavailable")
	}

	capacity := uint32(16)
	var layout jobObjectBasicProcessIDList
	listOffset := unsafe.Offsetof(layout.ProcessIDList)
	pointerSize := unsafe.Sizeof(uintptr(0))
	for {
		bufferSize := listOffset + uintptr(capacity)*pointerSize
		if bufferSize > uintptr(^uint32(0)) {
			return nil, fmt.Errorf("query managed job process identities: buffer size %d overflows DWORD", bufferSize)
		}
		buffer := make([]byte, int(bufferSize))
		list := (*jobObjectBasicProcessIDList)(unsafe.Pointer(&buffer[0]))
		err := windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicProcessIdList,
			uintptr(unsafe.Pointer(list)),
			uint32(bufferSize),
			nil,
		)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, fmt.Errorf("query managed job process identities: %w", err)
		}

		assigned := list.NumberOfAssignedProcesses
		listed := list.NumberOfProcessIDsInList
		if errors.Is(err, windows.ERROR_MORE_DATA) || listed < assigned {
			if assigned > capacity {
				capacity = assigned
			} else if capacity <= ^uint32(0)/2 {
				capacity *= 2
			} else {
				return nil, errors.New("query managed job process identities: process count overflow")
			}
			continue
		}
		if listed > capacity {
			return nil, fmt.Errorf(
				"query managed job process identities: returned %d processes in capacity %d",
				listed,
				capacity,
			)
		}

		rawIDs := unsafe.Slice(
			(*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(list))+listOffset)),
			int(listed),
		)
		processIDs := make([]uint32, 0, listed)
		for _, rawID := range rawIDs {
			if rawID > uintptr(^uint32(0)) {
				return nil, fmt.Errorf("query managed job process identities: process id %d overflows DWORD", rawID)
			}
			processIDs = append(processIDs, uint32(rawID))
		}

		return processIDs, nil
	}
}

func processBelongsToJob(process windows.Handle, job windows.Handle) (bool, error) {
	var result int32
	succeeded, _, callErr := isProcessInJobProc.Call(
		uintptr(process),
		uintptr(job),
		uintptr(unsafe.Pointer(&result)),
	)
	if succeeded == 0 {
		return false, fmt.Errorf("validate managed job process identity: %w", callErr)
	}

	return result != 0, nil
}

func waitForJobProcessObservations(observations map[uint32]jobProcessObservation) error {
	for processID, observation := range observations {
		event, err := windows.WaitForSingleObject(observation.handle, windows.INFINITE)
		if err != nil {
			return fmt.Errorf("wait for managed job process %d: %w", processID, err)
		}
		if event != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("wait for managed job process %d returned event %#x", processID, event)
		}
	}

	return nil
}

func closeJobProcessObservations(observations map[uint32]jobProcessObservation) error {
	var closeErrors []error
	for processID, observation := range observations {
		if !observation.owned || observation.handle == 0 {
			continue
		}
		if err := closeWindowsHandle(
			observation.handle,
			fmt.Sprintf("managed job process %d observation", processID),
		); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}

	return errors.Join(closeErrors...)
}

func (tree *managedProcessTree) observeDirectProcessExit() error {
	tree.handlesMu.RLock()
	process := tree.process
	waitForExitEvent := tree.waitForDirectProcessExitEvent
	tree.handlesMu.RUnlock()
	if process == 0 {
		if tree.attachErr != nil {
			return tree.attachErr
		}

		return errors.New("managed direct process handle is unavailable")
	}

	if waitForExitEvent == nil {
		waitForExitEvent = windows.WaitForSingleObject
	}
	event, err := waitForExitEvent(process, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for managed direct process: %w", err)
	}
	if event != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("wait for managed direct process returned event %#x", event)
	}

	return nil
}

// waitBeforeReap waits until the attach-started observer has seen direct exit
// and terminated the remaining Job members. Starting observation at attach is
// essential for streaming callers that read inherited pipes before they Wait.
func (tree *managedProcessTree) waitBeforeReap() error {
	if tree == nil {
		return nil
	}

	<-tree.teardownDone
	tree.teardownMu.Lock()
	defer tree.teardownMu.Unlock()

	return tree.teardownErr
}

func (tree *managedProcessTree) terminate() error {
	if tree == nil {
		return nil
	}

	<-tree.attachDone
	tree.terminateOnce.Do(func() {
		tree.handlesMu.RLock()
		attached := tree.attached
		job := tree.job
		tree.handlesMu.RUnlock()

		if !attached {
			tree.terminateErr = tree.killUnattachedDirectProcess()
			return
		}

		observations, _, captureErr := tree.captureJobProcessObservations(nil)
		tree.jobProcesses = observations
		terminateJobObject := tree.terminateJobObject
		if terminateJobObject == nil {
			terminateJobObject = terminateJob
		}
		terminationErr := terminateJobObject(job)
		if terminationErr == nil {
			tree.terminateErr = captureErr
			return
		}

		fallbackErr := tree.closeJobAfterTerminationFailure(job)
		if fallbackErr == nil {
			tree.terminateErr = errors.Join(captureErr, terminationErr)
			return
		}

		// If Job termination and its kill-on-close fallback both fail, only
		// the direct child can be stopped reliably. This unblocks the observer
		// without signaling any PID that was merely observed in a Job snapshot.
		directErr := normalizeManagedTerminationError(tree.killUnattachedDirectProcess())
		tree.terminateErr = errors.Join(captureErr, terminationErr, fallbackErr, directErr)
	})

	return tree.terminateErr
}

func (tree *managedProcessTree) closeJobAfterTerminationFailure(job windows.Handle) error {
	tree.handlesMu.Lock()
	defer tree.handlesMu.Unlock()

	if job == 0 || tree.job != job {
		tree.jobTerminationFallback = jobTerminationFallbackCloseFailed
		return errors.New("close managed process job after termination failure: owned job handle changed")
	}

	closeJob := tree.closeJobOnTerminationFailure
	if closeJob == nil {
		closeJob = windows.CloseHandle
	}
	if err := closeJob(job); err != nil {
		tree.jobTerminationFallback = jobTerminationFallbackCloseFailed
		return fmt.Errorf("close managed process job after termination failure: %w", err)
	}

	tree.job = 0
	tree.jobTerminationFallback = jobTerminationFallbackClosed
	return nil
}

func normalizeManagedTerminationError(err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}

	return err
}

func (tree *managedProcessTree) terminateAttachedJob() error {
	tree.handlesMu.RLock()
	defer tree.handlesMu.RUnlock()

	return terminateJob(tree.job)
}

func terminateJob(job windows.Handle) error {
	if job == 0 {
		return nil
	}
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return fmt.Errorf("terminate managed process job: %w", err)
	}

	return nil
}

func (tree *managedProcessTree) killUnattachedDirectProcess() error {
	if tree.cmd == nil || tree.cmd.Process == nil {
		return nil
	}
	if err := tree.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("terminate unattached direct process: %w", err)
	}

	return nil
}

func (tree *managedProcessTree) close() error {
	if tree == nil {
		return nil
	}

	<-tree.attachDone
	tree.closeOnce.Do(func() {
		tree.handlesMu.Lock()
		defer tree.handlesMu.Unlock()

		var closeErrors []error
		if tree.process != 0 {
			if err := windows.CloseHandle(tree.process); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close managed direct process handle: %w", err))
			}
			tree.process = 0
		}
		if tree.job != 0 {
			if err := windows.CloseHandle(tree.job); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close managed process job: %w", err))
			}
			tree.job = 0
		}

		tree.closeErr = errors.Join(closeErrors...)
	})

	return tree.closeErr
}

func resumeSuspendedProcess(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot process threads: %w", err)
	}

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		firstErr := fmt.Errorf("read first process thread: %w", err)
		return errors.Join(firstErr, closeWindowsHandle(snapshot, "thread snapshot"))
	}

	for {
		if entry.OwnerProcessID == processID {
			resumeErr := resumeSuspendedThread(entry.ThreadID)
			return errors.Join(resumeErr, closeWindowsHandle(snapshot, "thread snapshot"))
		}

		err := windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			nextErr := fmt.Errorf("read next process thread: %w", err)
			return errors.Join(nextErr, closeWindowsHandle(snapshot, "thread snapshot"))
		}
	}

	missingErr := fmt.Errorf("find suspended thread for process %d", processID)
	return errors.Join(missingErr, closeWindowsHandle(snapshot, "thread snapshot"))
}

func resumeSuspendedThread(threadID uint32) error {
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return fmt.Errorf("open suspended process thread: %w", err)
	}

	previousSuspendCount, resumeErr := windows.ResumeThread(thread)
	if resumeErr != nil {
		resumeErr = fmt.Errorf("resume suspended process thread: %w", resumeErr)
	} else if previousSuspendCount != 1 {
		resumeErr = fmt.Errorf("resume suspended process thread: previous suspend count was %d, expected 1", previousSuspendCount)
	}

	return errors.Join(resumeErr, closeWindowsHandle(thread, "process thread"))
}

func closeWindowsHandle(handle windows.Handle, name string) error {
	if handle == 0 {
		return nil
	}
	if err := windows.CloseHandle(handle); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}

	return nil
}
