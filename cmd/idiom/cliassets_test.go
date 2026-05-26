package main

import (
	"os"
	"path/filepath"
	"testing"
)

func cliAssetPath(elem ...string) string {
	parts := append([]string{"..", "..", "internal", "cliassets"}, elem...)
	return filepath.Join(parts...)
}

func readCLIAsset(t *testing.T, elem ...string) []byte {
	t.Helper()
	body, err := os.ReadFile(cliAssetPath(elem...))
	if err != nil {
		t.Fatalf("read CLI asset %s: %v", filepath.Join(elem...), err)
	}
	return body
}
