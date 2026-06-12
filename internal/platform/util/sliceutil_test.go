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

func TestEqualSlice(t *testing.T) {
	if !EqualSlice([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("expected equal")
	}

	if EqualSlice([]string{"a", "b"}, []string{"a"}) {
		t.Error("expected not equal (different lengths)")
	}

	if EqualSlice([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("expected not equal (different elements)")
	}
}

func TestUnique(t *testing.T) {
	result := Unique([]string{"a", "  b  ", "a", "", "b", "c"})
	if len(result) != 3 {
		t.Fatalf("Unique len = %d, want 3: %v", len(result), result)
	}

	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("Unique = %v, want [a b c]", result)
	}

	if len(Unique(nil)) != 0 {
		t.Error("Unique(nil) should return nil")
	}

	if Unique([]string{}) != nil {
		t.Error("Unique([]) should return nil")
	}
}

func TestMerge(t *testing.T) {
	result := Merge([]string{"a", "b"}, []string{"b", "c"}, nil)
	if len(result) != 3 {
		t.Fatalf("Merge len = %d, want 3: %v", len(result), result)
	}

	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("Merge = %v, want [a b c]", result)
	}

	if Merge() != nil {
		t.Error("Merge() should return nil")
	}
}

func TestSorted(t *testing.T) {
	result := Sorted([]string{"c", "a", "b", "a", ""})
	if len(result) != 3 {
		t.Fatalf("Sorted len = %d, want 3: %v", len(result), result)
	}

	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("Sorted = %v, want [a b c]", result)
	}
}

func TestRemove(t *testing.T) {
	result := Remove([]string{"a", "b", "c", "d"}, []string{"b", "d"})
	if len(result) != 2 || result[0] != "a" || result[1] != "c" {
		t.Errorf("Remove = %v, want [a c]", result)
	}
}

func TestRemoveEmptyRemove(t *testing.T) {
	result := Remove([]string{"a", "b"}, nil)
	if len(result) != 2 || result[0] != "a" || result[1] != "b" {
		t.Errorf("Remove with empty remove = %v, want [a b]", result)
	}
}

func TestRemoveEmptyValues(t *testing.T) {
	result := Remove(nil, []string{"a"})
	if len(result) != 0 {
		t.Errorf("Remove nil values = %v, want empty", result)
	}
}

func TestUniqueLimit(t *testing.T) {
	result := UniqueLimit([]string{"a", "b", "a", "c"}, 2)
	if len(result) != 2 {
		t.Fatalf("UniqueLimit len = %d, want 2: %v", len(result), result)
	}

	if result[0] != "a" || result[1] != "b" {
		t.Errorf("UniqueLimit = %v, want [a b]", result)
	}
}

func TestUniqueLimitBelowMax(t *testing.T) {
	original := []string{"a", "b"}
	result := UniqueLimit(original, 10)

	if len(result) != 2 {
		t.Fatalf("UniqueLimit len = %d, want 2", len(result))
	}
}

func TestUniqueLimitZeroMax(t *testing.T) {
	result := UniqueLimit([]string{"a", "b"}, 0)
	if len(result) != 2 {
		t.Fatalf("UniqueLimit with max=0 len = %d, want 2", len(result))
	}
}

func TestPrefix(t *testing.T) {
	original := []string{"a", "b", "c", "d", "e"}
	result := Prefix(original, 3)

	if len(result) != 3 || result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("Prefix = %v, want [a b c]", result)
	}

	if &result[0] != &original[0] {
		t.Error("Prefix should return original backing array")
	}
}

func TestPrefixBelowMax(t *testing.T) {
	original := []string{"a", "b"}
	result := Prefix(original, 10)

	if len(result) != 2 {
		t.Fatalf("Prefix below max len = %d, want 2", len(result))
	}

	if &result[0] != &original[0] {
		t.Error("Prefix should return original slice when below max")
	}
}

func TestPrefixZeroMax(t *testing.T) {
	original := []string{"a", "b"}
	result := Prefix(original, 0)

	if len(result) != 2 {
		t.Fatalf("Prefix with max=0 len = %d, want 2", len(result))
	}
}

func TestPrefixNegativeMax(t *testing.T) {
	original := []string{"a", "b"}
	result := Prefix(original, -1)

	if len(result) != 2 {
		t.Fatalf("Prefix with max=-1 len = %d, want 2", len(result))
	}
}
