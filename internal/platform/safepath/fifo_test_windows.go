//go:build windows

package safepath

import "errors"

func createNamedFIFO(string) error {
	return errors.New("named FIFOs are not supported by this test on Windows")
}
