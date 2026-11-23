package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring; "" means stdout must be empty
		wantStderr string // substring; "" means stderr must be empty
	}{
		{name: "version", args: []string{"-version"}, wantCode: exitOK, wantStdout: "drydock "},
		{name: "version double dash", args: []string{"--version"}, wantCode: exitOK, wantStdout: "drydock "},
		{name: "help", args: []string{"-h"}, wantCode: exitOK, wantStderr: "Usage: drydock"},
		{name: "help long", args: []string{"-help"}, wantCode: exitOK, wantStderr: "Usage: drydock"},
		{name: "unknown flag", args: []string{"-nope"}, wantCode: exitUsage, wantStderr: "flag provided but not defined"},
		{name: "positional argument", args: []string{"extra"}, wantCode: exitUsage, wantStderr: `unexpected argument "extra"`},
		{name: "version with positional argument", args: []string{"-version", "extra"}, wantCode: exitUsage, wantStderr: `unexpected argument "extra"`},
		{name: "no arguments", args: nil, wantCode: exitError, wantStderr: "not implemented yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tt.wantCode, stderr.String())
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

// checkOutput asserts that got contains want, or that got is empty when want
// is empty.
func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("unexpected %s: %q", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}
