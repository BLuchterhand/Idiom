package transcript

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/atomicfile"
)

type Message struct {
	Role    string
	Content string
}

func SaveMarkdown(dir string, messages []Message) (string, error) {
	return saveMarkdown(dir, messages, time.Now())
}

func saveMarkdown(dir string, messages []Message, savedAt time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create transcript dir: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open transcript dir: %w", err)
	}
	defer root.Close()

	body := renderMarkdown(messages, savedAt)
	for {
		filename, err := newTranscriptFilename(savedAt)
		if err != nil {
			return "", err
		}

		published, err := publishMarkdown(root, filename, body)
		if !published && err == nil {
			continue
		}

		path := filepath.Join(dir, filename)
		if err != nil {
			if published {
				return path, fmt.Errorf("write transcript: %w", err)
			}
			return "", fmt.Errorf("write transcript: %w", err)
		}
		return path, nil
	}
}

func renderMarkdown(messages []Message, savedAt time.Time) []byte {
	var b strings.Builder

	b.WriteString("# Idiom Transcript\n\n")
	b.WriteString("Saved: " + savedAt.Format(time.RFC3339) + "\n\n")

	for _, msg := range messages {
		role := transcriptTitle(msg.Role)
		if role == "" {
			role = "Message"
		}

		b.WriteString("## " + role + "\n\n")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}

	return []byte(b.String())
}

func newTranscriptFilename(savedAt time.Time) (string, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", fmt.Errorf("generate transcript identity: %w", err)
	}

	return fmt.Sprintf("%s-%x.md", savedAt.Format("20060102-150405.000000000"), identity), nil
}

func publishMarkdown(root *os.Root, filename string, body []byte) (bool, error) {
	tempName := "." + filename + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	tempPresent := true
	defer func() {
		if tempPresent {
			_ = root.Remove(tempName)
		}
	}()

	if err := writeAndSyncTranscript(temp, body); err != nil {
		return false, err
	}
	if err := root.Link(tempName, filename); os.IsExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	removeErr := root.Remove(tempName)
	if removeErr == nil {
		tempPresent = false
	}
	syncErr := atomicfile.SyncDirRoot(root, ".")
	if err := errors.Join(removeErr, syncErr); err != nil {
		return true, &atomicfile.PostCommitError{
			Op:  "transcript published but directory finalization failed",
			Err: err,
		}
	}

	return true, nil
}

func writeAndSyncTranscript(file *os.File, body []byte) error {
	n, writeErr := file.Write(body)
	if writeErr == nil && n != len(body) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		_ = file.Close()
		return writeErr
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync transcript temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close transcript temp file: %w", err)
	}
	return nil
}

func transcriptTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	return strings.ToUpper(value[:1]) + value[1:]
}
