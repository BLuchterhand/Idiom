package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

const defaultScanFileMaxLineBytes = 4 << 20

func ScanFile(path string, fn func(json.RawMessage) error, maxLineBytes ...int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return ScanReader(f, fn, maxLineBytes...)
}

func ScanReader(reader io.Reader, fn func(json.RawMessage) error, maxLineBytes ...int) error {
	if reader == nil {
		return os.ErrInvalid
	}
	max := defaultScanFileMaxLineBytes
	if len(maxLineBytes) > 0 && maxLineBytes[0] > 0 {
		max = maxLineBytes[0]
	}

	scanner := bufio.NewScanner(reader)
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
