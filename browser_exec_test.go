package main

import (
	"errors"
	"testing"
)

// TestIsExecNotFound verifies we correctly distinguish a missing-Chrome launch
// failure (actionable: tell the admin to install Chrome) from a genuine runtime
// crash (not actionable from the UI). This guards the clear-error message added
// to handleBrowserLoginStart.
func TestIsExecNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"exec not found in PATH", errors.New(`exec: "chrome": executable file not found in $PATH`), true},
		{"no such file", errors.New(`fork/exec /usr/bin/chromium: no such file or directory`), true},
		{"exec prefix", errors.New(`exec: "chromium-browser": cannot find`), true},
		{"chrome not found literal", errors.New(`chrome: not found`), true},
		{"chromium not found literal", errors.New(`chromium: not found`), true},
		{"runtime crash", errors.New(`chrome exited unexpectedly: signal: killed`), false},
		{"context deadline", errors.New(`context deadline exceeded`), false},
		{"sandbox error", errors.New(`chrome failed to start: cannot open display`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isExecNotFound(c.err); got != c.want {
				t.Fatalf("isExecNotFound(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
