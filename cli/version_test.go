package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A script captures the version with $(projects version), which reads stdout
// alone, so the version has to land there and nothing on stderr.
func TestVersionWritesTheVersionToStdout(t *testing.T) {
	cmd := VersionCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != Version() {
		t.Errorf("stdout = %q, want %q", got, Version())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}
