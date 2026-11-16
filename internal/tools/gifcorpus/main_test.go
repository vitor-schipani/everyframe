package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		want       int
		wantStdout string
		wantStderr string
	}{
		{"no command", nil, 2, "", "usage:"},
		{"help", []string{"help"}, 0, "usage:", ""},
		{"unknown command", []string{"bogus"}, 2, "", `unknown command "bogus"`},
		{"command help", []string{"fetch", "-h"}, 0, "", "-limit"},
		{"bad flag", []string{"verify", "-nope"}, 1, "", "-nope"},
		{"missing manifest", []string{"verify", "-manifest", "does-not-exist.json"}, 1, "", "does-not-exist.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(context.Background(), tc.args, &stdout, &stderr); got != tc.want {
				t.Errorf("exit code = %d, want %d (stderr: %s)", got, tc.want, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tc.wantStdout)
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

func TestRunFailsWhenOutputCannotBeWritten(t *testing.T) {
	var stderr bytes.Buffer
	if got := run(context.Background(), []string{"help"}, failingWriter{}, &stderr); got != 1 {
		t.Errorf("exit code = %d, want 1 when stdout is broken", got)
	}
}
