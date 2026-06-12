package util

import "strings"

// MultiFlag is a flag.Value that accumulates repeated string values.
// Use with flag.FlagSet.Var to accept -f a -f b -f c.
type MultiFlag []string

func (m *MultiFlag) String() string {
	if m == nil || len(*m) == 0 {
		return ""
	}

	return strings.Join(*m, ",")
}

func (m *MultiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
