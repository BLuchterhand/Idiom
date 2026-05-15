package util

import (
	"bufio"
	"encoding/json"
	"os"
)

func ScanFile(path string, fn func(json.RawMessage) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := fn(json.RawMessage(line)); err != nil {
			return err
		}
	}
	return scanner.Err()
}
