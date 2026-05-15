package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDir(t *testing.T) {
	tmp := t.TempDir()

	dir := filepath.Join(tmp, "a", "b", "c")
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat created dir: %v", err)
	}
	if !st.IsDir() {
		t.Fatal("expected directory, got file")
	}
}

func TestEnsureDirIdempotent(t *testing.T) {
	tmp := t.TempDir()

	dir := filepath.Join(tmp, "exists")
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("first EnsureDir: %v", err)
	}
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("second EnsureDir: %v", err)
	}
}

func TestEnsureDirEmptyPath(t *testing.T) {
	err := EnsureDir("")
	if err == nil {
		t.Log("EnsureDir empty string succeeded (OS-dependent)")
	} else {
		t.Logf("EnsureDir empty string errored as expected: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sub", "dir", "file.txt")
	data := []byte("hello world")

	if err := WriteFile(path, data); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("WriteFile content = %q, want %q", got, data)
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "file.txt")

	if err := WriteFile(path, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFile(path, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("after overwrite = %q, want %q", got, "second")
	}
}

func TestWriteFileEmptyData(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "deep", "nested", "empty")

	if err := WriteFile(path, nil); err != nil {
		t.Fatalf("WriteFile empty: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("empty file size = %d, want 0", info.Size())
	}
}

func TestWriteFilePermissionBits(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "perm.txt")

	if err := WriteFile(path, []byte("x")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o644 {
		t.Errorf("permissions = %04o, want 0644", perm)
	}
}

func TestWriteFileInvalidPath(t *testing.T) {
	err := WriteFile("/dev/null/impossible/path", []byte("x"))
	if err == nil {
		t.Error("expected error for impossible path")
	}
}

func TestOpenAppend(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nested", "log.txt")

	f1, err := OpenAppend(path)
	if err != nil {
		t.Fatalf("first OpenAppend: %v", err)
	}
	if _, err := f1.WriteString("line1\n"); err != nil {
		f1.Close()
		t.Fatal(err)
	}
	f1.Close()

	f2, err := OpenAppend(path)
	if err != nil {
		t.Fatalf("second OpenAppend: %v", err)
	}
	if _, err := f2.WriteString("line2\n"); err != nil {
		f2.Close()
		t.Fatal(err)
	}
	f2.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\n"
	if string(got) != want {
		t.Errorf("appended content = %q, want %q", string(got), want)
	}
}

func TestOpenAppendCreatesFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "new", "dir", "file.log")

	f, err := OpenAppend(path)
	if err != nil {
		t.Fatalf("OpenAppend on nonexistent path: %v", err)
	}
	f.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}
}

func TestOpenAppendDoesNotTruncate(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "preserve.txt")

	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "existing") {
		t.Errorf("OpenAppend truncated file, got %q", got)
	}
}

func TestWriteFileLargeData(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.bin")
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := WriteFile(path, data); err != nil {
		t.Fatalf("WriteFile 1MB: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(data) {
		t.Errorf("size = %d, want %d", len(got), len(data))
	}
}

func BenchmarkWriteFile(b *testing.B) {
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.txt")
	data := []byte(strings.Repeat("x", 1024))
	for b.Loop() {
		if err := WriteFile(path, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAppend(b *testing.B) {
	tmp := b.TempDir()
	path := filepath.Join(tmp, "bench.log")
	f, err := OpenAppend(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	line := []byte("benchmark line\n")
	for b.Loop() {
		if _, err := f.Write(line); err != nil {
			b.Fatal(err)
		}
	}
}
