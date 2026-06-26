package util

import "testing"

func TestRunningUnderGoTest(t *testing.T) {
	if !RunningUnderGoTest() {
		t.Fatal("go test process should be detected")
	}
}
