package util

import "testing"

func TestAppendUnique(t *testing.T) {
	list := []string{"a", "b"}
	result := AppendUnique(list, "c")
	if len(result) != 3 || result[2] != "c" {
		t.Errorf("AppendUnique missing new element: %v", result)
	}
}

func TestAppendUniqueDuplicate(t *testing.T) {
	list := []string{"a", "b"}
	result := AppendUnique(list, "a")
	if len(result) != 2 {
		t.Errorf("AppendUnique should not append duplicate: %v", result)
	}
}

func TestAppendUniqueEmpty(t *testing.T) {
	result := AppendUnique(nil, "x")
	if len(result) != 1 || result[0] != "x" {
		t.Errorf("AppendUnique on nil slice: %v", result)
	}
}

func TestAppendUniqueEmptyValue(t *testing.T) {
	list := []string{"a"}
	result := AppendUnique(list, "")
	if len(result) != 1 {
		t.Errorf("AppendUnique should skip empty string: %v", result)
	}
}
