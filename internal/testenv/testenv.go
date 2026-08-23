// Package testenv emulates some testenv properties to reduce
// the amount of deleted line with respect to upstream.
package testenv

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// MustHaveExec always skips the current test.
func MustHaveExec(t testing.TB) {
	t.Skip("testenv.MustHaveExec is not enabled in this fork")
}

// SkipFlay skips a flaky test.
func SkipFlaky(t testing.TB, issue int) {
	t.Skip("testenv.SkipFlaky: skipping flaky test", issue)
}

// HasSrc always returns false.
func HasSrc() bool {
	return false
}

// GoToolPath returns the Go tool path.
func GoToolPath(t testing.TB) string {
	return "go"
}

// Builder always returns the empty string.
func Builder() string {
	return ""
}

func CommandContext(t testing.TB, ctx context.Context, name string, args ...string) *exec.Cmd {
	t.Skip("testenv.CommandContext is not enabled in this fork")
	return &exec.Cmd{}
}

func Command(t testing.TB, name string, args ...string) *exec.Cmd {
	return CommandContext(t, context.Background(), name, args...)
}

// MustHaveSource skips the test if Go source is not available.
func MustHaveSource(t testing.TB) {
	t.Skip("testenv.MustHaveSource is not enabled in this fork")
}

// Executable returns the path to the current test binary.
func Executable(t testing.TB) string {
	path, err := os.Executable()
	if err != nil {
		t.Skipf("testenv.Executable: %v", err)
	}
	return path
}

// CleanCmdEnv returns cmd with a reduced environment.
func CleanCmdEnv(cmd *exec.Cmd) *exec.Cmd {
	if cmd.Env != nil {
		panic("environment already set")
	}
	for _, env := range os.Environ() {
		cmd.Env = append(cmd.Env, env)
	}
	return cmd
}
