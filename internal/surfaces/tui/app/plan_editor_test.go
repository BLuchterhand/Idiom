package app

import (
	"reflect"
	"testing"
)

func TestSplitEditorCommandParsesQuotedArgsWithoutShell(t *testing.T) {
	args, err := splitEditorCommand(`code --wait "plan file.md"`)
	if err != nil {
		t.Fatalf("splitEditorCommand: %v", err)
	}

	want := []string{"code", "--wait", "plan file.md"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSplitEditorCommandTreatsShellMetacharactersAsArgs(t *testing.T) {
	args, err := splitEditorCommand(`vi; touch injected`)
	if err != nil {
		t.Fatalf("splitEditorCommand: %v", err)
	}

	want := []string{"vi;", "touch", "injected"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSplitEditorCommandRejectsUnterminatedQuote(t *testing.T) {
	if _, err := splitEditorCommand(`code "unterminated`); err == nil {
		t.Fatal("expected unterminated quote error")
	}
}
