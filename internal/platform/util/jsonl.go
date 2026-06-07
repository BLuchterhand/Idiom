package util

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
)

const defaultScanFileMaxLineBytes = 4 << 20

func ScanFile(path string, fn func(json.RawMessage) error, maxLineBytes ...int) error {
	max := defaultScanFileMaxLineBytes
	if len(maxLineBytes) > 0 && maxLineBytes[0] > 0 {
		max = maxLineBytes[0]
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), max)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(json.RawMessage(line)); err != nil {
			return err
		}
	}
	return scanner.Err()
}
