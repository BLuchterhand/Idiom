package featureplanner

type ReplayError struct {
	Message string
}

func (e ReplayError) Error() string {
	return e.Message
}
