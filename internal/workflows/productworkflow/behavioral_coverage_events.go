package productworkflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/atomicfile"
)

type BehavioralCoveragePromotionEvent struct {
	Event            string `json:"event"`
	EventID          string `json:"event_id"`
	State            string `json:"state"`
	CommitStatus     string `json:"commit_status,omitempty"`
	EffortID         string `json:"effort_id,omitempty"`
	TaskID           string `json:"task_id,omitempty"`
	Digest           string `json:"digest,omitempty"`
	SupersededDigest string `json:"superseded_digest,omitempty"`
	Timestamp        string `json:"timestamp"`
	Source           string `json:"source"`
}

type BehavioralCoveragePromotionCommitError struct {
	File string
	Err  error
}

func (e BehavioralCoveragePromotionCommitError) Error() string {
	if strings.TrimSpace(e.File) == "" {
		return e.Err.Error()
	}
	return strings.TrimSpace(e.File) + ": " + e.Err.Error()
}

func (e BehavioralCoveragePromotionCommitError) Unwrap() error {
	return e.Err
}

func commitBehavioralCoveragePromotion(
	effortRoot string,
	expectedGeneration tddGenerationToken,
	contract BehavioralCoverageContract,
) (BehavioralCoveragePromotionResult, error) {
	lease, err := acquireTDDMutationLease(effortRoot, expectedGeneration)
	if err != nil {
		return BehavioralCoveragePromotionResult{}, err
	}
	defer lease.Close()
	eventsPath, err := productWorkflowEventsPath(lease.workflowState)
	if err != nil {
		return BehavioralCoveragePromotionResult{}, fmt.Errorf("resolve behavioral coverage event ledger: %w", err)
	}
	if len(lease.currentBody) == 0 {
		return BehavioralCoveragePromotionResult{}, errors.New("behavioral coverage promotion requires an existing tdd.json")
	}
	ledger, err := ParseTDDLedgerJSON(lease.currentBody)
	if err != nil {
		return BehavioralCoveragePromotionResult{}, fmt.Errorf("parse current tdd.json: %w", err)
	}
	result, err := prepareBehavioralCoveragePromotion(lease.workflowState, ledger, contract)
	if err != nil {
		return result, err
	}
	eventID := behavioralCoveragePromotionEventID(result.Digest)
	if eventID == "" || result.Ledger.BehavioralCoverage == nil {
		return blockedBehavioralCoveragePromotion(result, false), errors.New("behavioral coverage promotion could not mint provenance")
	}
	accepted := *result.Ledger.BehavioralCoverage
	accepted.EventID = eventID
	accepted.PromotionState = BehavioralCoveragePromotionStateAccepted
	result.Ledger.BehavioralCoverage = &accepted
	result.State = BehavioralCoveragePromotionStateAccepted
	result.EventID = eventID
	result.CommitStatus = BehavioralCoveragePromotionCommitStatusWritePending
	committedEventResult := result
	committedEventResult.CommitStatus = BehavioralCoveragePromotionCommitStatusCommitted
	event, eventType, err := newBehavioralCoveragePromotionEvent(committedEventResult)
	if err != nil {
		return blockedBehavioralCoveragePromotion(result, false), err
	}
	if _, _, err := inspectBehavioralCoveragePromotionEventAuthority(lease.lease, eventsPath, eventType, event); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), fmt.Errorf("reserve behavioral coverage promotion event_id: %w", err)
	}

	if err := validateTDDLedger(result.Ledger); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), fmt.Errorf("validate promoted tdd.json: %w", err)
	}
	data, err := json.MarshalIndent(result.Ledger, "", "  ")
	if err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}
	if err := lease.lease.ValidatePathIdentity(); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), fmt.Errorf("validate behavioral coverage effort root: %w", err)
	}
	writeErr := writeEffortArtifactAtomicRoot(lease.lease.root, "tdd.json", append(data, '\n'), 0o644)
	if !artifactWriteCommitted(writeErr) {
		err := writeErr
		if err == nil {
			err = errors.New("tdd.json write did not commit")
		}
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}

	result.CommitStatus = BehavioralCoveragePromotionCommitStatusCommitted
	result.LedgerCommitted = true
	if writeErr != nil {
		result.CommitWarning = writeErr.Error()
	}
	if err := appendBehavioralCoveragePromotionEvent(lease.lease, eventsPath, eventType, event); err != nil {
		result.AuditStatus = BehavioralCoveragePromotionAuditStatusDegraded
		result.AuditError = err.Error()
		return result, nil
	}
	result.AuditStatus = BehavioralCoveragePromotionAuditStatusRecorded

	return result, nil
}

func blockedBehavioralCoveragePromotion(result BehavioralCoveragePromotionResult, ledgerCommitted bool) BehavioralCoveragePromotionResult {
	result.CommitStatus = BehavioralCoveragePromotionCommitStatusBlocked
	result.LedgerCommitted = ledgerCommitted
	if !ledgerCommitted {
		result.State = BehavioralCoveragePromotionStateRejected
		result.EventID = ""
		if result.Ledger.BehavioralCoverage != nil {
			candidate := *result.Ledger.BehavioralCoverage
			candidate.EventID = ""
			candidate.PromotionState = ""
			result.Ledger.BehavioralCoverage = &candidate
		}
	}
	if strings.TrimSpace(result.AuditStatus) == "" {
		result.AuditStatus = BehavioralCoveragePromotionAuditStatusNotAttempted
	}
	return result
}

func appendBehavioralCoveragePromotionEvent(
	lease *productWorkflowMutationLease,
	eventsPath string,
	eventType ProductWorkflowEventType,
	event BehavioralCoveragePromotionEvent,
) error {
	if lease == nil || lease.root == nil {
		return errors.New("behavioral coverage promotion event lease is required")
	}
	cleanEventsPath, ok := cleanEffortRelativePath(eventsPath)
	if !ok {
		return fmt.Errorf("behavioral coverage promotion event path %q must be effort-relative", eventsPath)
	}
	eventsPath = cleanEventsPath
	existing, alreadyRecorded, err := inspectBehavioralCoveragePromotionEventAuthority(lease, eventsPath, eventType, event)
	if err != nil {
		return err
	}
	if alreadyRecorded {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	line := append([]byte(nil), data...)
	line = append(line, '\n')
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		line = append([]byte{'\n'}, line...)
	}

	return appendBehavioralCoverageEventLine(lease, eventsPath, line)
}

func newBehavioralCoveragePromotionEvent(
	result BehavioralCoveragePromotionResult,
) (BehavioralCoveragePromotionEvent, ProductWorkflowEventType, error) {
	if strings.TrimSpace(result.EventID) == "" {
		return BehavioralCoveragePromotionEvent{}, "", errors.New("behavioral coverage promotion event_id is required")
	}
	eventType, ok := ProductWorkflowBehavioralCoverageEvent(result.State)
	if !ok {
		return BehavioralCoveragePromotionEvent{}, "", fmt.Errorf(
			"behavioral coverage promotion state %q has no declared event type",
			strings.TrimSpace(result.State),
		)
	}
	event := BehavioralCoveragePromotionEvent{
		Event:            string(eventType),
		EventID:          strings.TrimSpace(result.EventID),
		State:            strings.TrimSpace(result.State),
		CommitStatus:     strings.TrimSpace(result.CommitStatus),
		Digest:           strings.TrimSpace(result.Digest),
		SupersededDigest: strings.TrimSpace(result.SupersededDigest),
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:           string(ProductWorkflowEventSourceTDDBriefPromotion),
	}
	if result.Ledger.BehavioralCoverage != nil {
		event.EffortID = strings.TrimSpace(result.Ledger.BehavioralCoverage.EffortID)
		event.TaskID = strings.TrimSpace(result.Ledger.BehavioralCoverage.TaskID)
	}
	if event.EffortID == "" {
		event.EffortID = strings.TrimSpace(result.Ledger.EffortID)
	}
	if event.TaskID == "" {
		event.TaskID = strings.TrimSpace(result.Ledger.TaskID)
	}

	return event, eventType, nil
}

func inspectBehavioralCoveragePromotionEventAuthority(
	lease *productWorkflowMutationLease,
	eventsPath string,
	eventType ProductWorkflowEventType,
	event BehavioralCoveragePromotionEvent,
) ([]byte, bool, error) {
	if lease == nil || lease.root == nil {
		return nil, false, errors.New("behavioral coverage promotion event lease is required")
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return nil, false, fmt.Errorf("validate product event audit effort root: %w", err)
	}
	existing, present, err := readWorkflowArtifactSnapshot(lease.root, eventsPath)
	if err != nil {
		return nil, false, err
	}
	if !present {
		existing = nil
	}
	if _, err := ParseBehavioralCoveragePromotionEventsJSONL(existing); err != nil {
		return nil, false, fmt.Errorf("parse existing behavioral coverage event stream: %w", err)
	}
	matches, err := productWorkflowEventEnvelopesByID(existing, event.EventID)
	if err != nil {
		return nil, false, fmt.Errorf("inspect existing product workflow event ids: %w", err)
	}
	for _, match := range matches {
		if match.Event != eventType {
			return nil, false, fmt.Errorf(
				"behavioral coverage event_id %q already belongs to event type %q",
				event.EventID,
				match.Event,
			)
		}
		var prior BehavioralCoveragePromotionEvent
		if err := json.Unmarshal(match.raw, &prior); err != nil {
			return nil, false, fmt.Errorf("parse matching behavioral coverage event line %d: %w", match.LineNumber, err)
		}
		if !sameBehavioralCoveragePromotionEventAuthority(prior, event) {
			return nil, false, fmt.Errorf("behavioral coverage event_id %q already records conflicting promotion authority", event.EventID)
		}
	}

	return existing, len(matches) > 0, nil
}

func sameBehavioralCoveragePromotionEventAuthority(left, right BehavioralCoveragePromotionEvent) bool {
	return strings.TrimSpace(left.Event) == strings.TrimSpace(right.Event) &&
		strings.TrimSpace(left.EventID) == strings.TrimSpace(right.EventID) &&
		strings.TrimSpace(left.State) == strings.TrimSpace(right.State) &&
		strings.TrimSpace(left.CommitStatus) == strings.TrimSpace(right.CommitStatus) &&
		strings.TrimSpace(left.EffortID) == strings.TrimSpace(right.EffortID) &&
		strings.TrimSpace(left.TaskID) == strings.TrimSpace(right.TaskID) &&
		strings.TrimSpace(left.Digest) == strings.TrimSpace(right.Digest) &&
		strings.TrimSpace(left.SupersededDigest) == strings.TrimSpace(right.SupersededDigest) &&
		strings.TrimSpace(left.Source) == strings.TrimSpace(right.Source)
}

func appendBehavioralCoverageEventLine(lease *productWorkflowMutationLease, eventsPath string, line []byte) error {
	if lease == nil || lease.root == nil {
		return errors.New("behavioral coverage promotion event lease is required")
	}
	root := lease.root
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate product event audit effort root before preparing append: %w", err)
	}
	if err := ensureEffortArtifactParents(root, eventsPath); err != nil {
		return fmt.Errorf("prepare behavioral coverage event stream parent: %w", err)
	}
	if err := validateEffortArtifactPath(root, eventsPath, effortArtifactMayBeMissing); err != nil {
		return err
	}
	existed := true
	if _, err := root.Lstat(eventsPath); errors.Is(err, os.ErrNotExist) {
		existed = false
	} else if err != nil {
		return fmt.Errorf("inspect behavioral coverage event stream: %w", err)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate product event audit effort root before append: %w", err)
	}

	file, err := root.OpenFile(eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open behavioral coverage event stream: %w", err)
	}
	pathInfo, pathErr := root.Lstat(eventsPath)
	fileInfo, fileErr := file.Stat()
	if pathErr != nil || fileErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		_ = file.Close()
		if pathErr != nil {
			return fmt.Errorf("inspect behavioral coverage event stream: %w", pathErr)
		}
		if fileErr != nil {
			return fmt.Errorf("inspect open behavioral coverage event stream: %w", fileErr)
		}
		return errors.New("behavioral coverage event stream changed while opening")
	}

	n, writeErr := file.Write(line)
	if writeErr == nil && n != len(line) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		_ = file.Close()
		if n > 0 {
			return &atomicfile.PostCommitError{Op: "behavioral coverage event append partially committed", Err: writeErr}
		}
		return writeErr
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return &atomicfile.PostCommitError{Op: "behavioral coverage event append committed but file sync failed", Err: err}
	}
	if err := file.Close(); err != nil {
		return &atomicfile.PostCommitError{Op: "behavioral coverage event append committed but close failed", Err: err}
	}
	after, err := root.Lstat(eventsPath)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(fileInfo, after) {
		if err == nil {
			err = errors.New("event stream identity changed")
		}
		return &atomicfile.PostCommitError{Op: "behavioral coverage event append committed but target validation failed", Err: err}
	}
	if !existed {
		if err := atomicfile.SyncDirRoot(root, filepath.Dir(eventsPath)); err != nil {
			return &atomicfile.PostCommitError{Op: "behavioral coverage event append committed but directory sync failed", Err: err}
		}
	}

	return nil
}

func ParseBehavioralCoveragePromotionEventsJSONL(data []byte) ([]BehavioralCoveragePromotionEvent, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var events []BehavioralCoveragePromotionEvent
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event BehavioralCoveragePromotionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		eventType := ProductWorkflowEventType(strings.TrimSpace(event.Event))
		if KnownProductWorkflowBehavioralCoverageEvent(eventType) {
			events = append(events, event)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(event.Event), string(ProductWorkflowEventSourceBehavioralCoverage)+"_") {
			return nil, fmt.Errorf("line %d: unknown behavioral coverage event %q", lineNo, event.Event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func AcceptedBehavioralCoveragePromotionEvent(events []BehavioralCoveragePromotionEvent, digest string) *BehavioralCoveragePromotionEvent {
	digest = strings.TrimSpace(digest)
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		acceptedEvent, _ := ProductWorkflowBehavioralCoverageEvent(BehavioralCoveragePromotionStateAccepted)
		if strings.TrimSpace(event.Event) != string(acceptedEvent) {
			continue
		}
		if strings.TrimSpace(event.State) != BehavioralCoveragePromotionStateAccepted {
			continue
		}
		commitStatus := strings.TrimSpace(event.CommitStatus)
		if commitStatus != "" && commitStatus != BehavioralCoveragePromotionCommitStatusCommitted {
			continue
		}
		if digest != "" && strings.TrimSpace(event.Digest) != digest {
			continue
		}
		if strings.TrimSpace(event.EventID) == "" {
			continue
		}
		return &events[i]
	}

	return nil
}
