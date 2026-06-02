package featureplanner

import "fmt"

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
