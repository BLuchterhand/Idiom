package util

func Ptr[T any](v T) *T {
	return &v
}

func ClonePtr[T any](v *T) *T {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}
