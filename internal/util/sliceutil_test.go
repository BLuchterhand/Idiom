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

func TestLimitSliceShorterThanLimit(t *testing.T) {
	original := []string{"a", "b", "c"}
	result := LimitSlice(original, 10)
	if len(result) != 3 {
		t.Fatalf("LimitSlice len = %d, want 3", len(result))
	}
	if &result[0] == &original[0] {
		t.Error("LimitSlice should return a copy")
	}
}

func TestLimitSliceExactlyAtLimit(t *testing.T) {
	original := []string{"a", "b", "c"}
	result := LimitSlice(original, 3)
	if len(result) != 3 {
		t.Fatalf("LimitSlice len = %d, want 3", len(result))
	}
	if &result[0] == &original[0] {
		t.Error("LimitSlice should return a copy")
	}
}

func TestLimitSliceLongerThanLimit(t *testing.T) {
	result := LimitSlice([]string{"a", "b", "c", "d", "e"}, 3)
	if len(result) != 3 || result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("LimitSlice should truncate: %v", result)
	}
}

func TestLimitSliceZeroLimit(t *testing.T) {
	original := []string{"a", "b"}
	result := LimitSlice(original, 0)
	if len(result) != 2 {
		t.Fatalf("LimitSlice with limit=0 len = %d, want 2", len(result))
	}
}

func TestLimitSliceNegativeLimit(t *testing.T) {
	original := []string{"a", "b"}
	result := LimitSlice(original, -5)
	if len(result) != 2 {
		t.Fatalf("LimitSlice with negative limit len = %d, want 2", len(result))
	}
}

func TestLimitSliceNil(t *testing.T) {
	result := LimitSlice(nil, 5)
	if result != nil {
		t.Errorf("LimitSlice(nil) = %v, want nil", result)
	}
}

func TestLimitSliceEmpty(t *testing.T) {
	result := LimitSlice([]string{}, 5)
	if len(result) != 0 {
		t.Errorf("LimitSlice([]) len = %d, want 0", len(result))
	}
}
