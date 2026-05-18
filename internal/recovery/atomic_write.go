package recovery

import (
	"fmt"
	"io"
	"os"
)

func writeRecoveryTempFileExclusive(tmpPath string, body []byte) error {
	file, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	wrote := false
	defer func() {
		if !wrote {
			_ = os.Remove(tmpPath)
		}
	}()
	n, err := file.Write(body)
	if err != nil {
		_ = file.Close()
		return err
	}
	if n != len(body) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	wrote = true
	return nil
}
