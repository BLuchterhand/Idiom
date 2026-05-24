package featureplanner

import (
	"errors"
	"fmt"
)

type ActionError struct {
	Action string
	State  string
}

func (e ActionError) Error() string {
	return fmt.Sprintf("action %q is not valid for state %q", e.Action, e.State)
}

type ReplayError struct {
	Message string
}

func (e ReplayError) Error() string {
	return e.Message
}

func errInvalidAction(action, state string) error {
	return ActionError{Action: action, State: state}
}

func errReplayf(format string, args ...any) error {
	return ReplayError{Message: fmt.Sprintf(format, args...)}
}

func errorsJoin(errs []error) error {
	return errors.Join(errs...)
}
