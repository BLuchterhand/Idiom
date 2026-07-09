package taskledger

import "strings"

// Status is the wire-visible lifecycle state of a task-ledger item.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusBlocked    Status = "blocked"
	StatusCancelled  Status = "cancelled"
)

var statusValues = [...]Status{
	StatusPending,
	StatusInProgress,
	StatusCompleted,
	StatusBlocked,
	StatusCancelled,
}

// Parse normalizes user-authored status text and reports whether it belongs to
// the task-ledger lifecycle.
func Parse(value string) (Status, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	status := Status(normalized)
	return status, status.Valid()
}

// Values returns the wire values in schema display order.
func Values() []string {
	values := make([]string, 0, len(statusValues))
	for _, status := range statusValues {
		values = append(values, string(status))
	}
	return values
}

func (status Status) Valid() bool {
	switch status {
	case StatusPending, StatusInProgress, StatusCompleted, StatusBlocked, StatusCancelled:
		return true
	default:
		return false
	}
}

func (status Status) Open() bool {
	return status == StatusPending || status == StatusInProgress
}

func (status Status) Finished() bool {
	return status == StatusCompleted || status == StatusCancelled
}
