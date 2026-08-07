package speech

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
)

func TestParseByteCount(t *testing.T) {
	value, err := parseByteCount(" 4096\r\n")
	if err != nil || value != 4096 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	for _, input := range []string{"", "nope", "-1"} {
		if _, err := parseByteCount(input); err == nil {
			t.Fatalf("invalid byte count accepted: %q", input)
		}
	}
}

func TestAvailableDiskBytesCurrentDirectory(t *testing.T) {
	bytes, err := availableDiskBytes(t.TempDir())
	if err != nil || bytes <= 0 {
		t.Fatalf("bytes=%d err=%v", bytes, err)
	}
}

func TestAvailableDiskBytesRejectsCommandAndFormatErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix df parser test")
	}
	old := commandContext
	t.Cleanup(func() { commandContext = old })
	for _, script := range []string{"exit 2", "printf 'bad response\\n'", "printf 'fs 1 2 nope /\\n'"} {
		commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", script)
		}
		if _, err := availableDiskBytes("."); err == nil {
			t.Fatalf("script %q accepted", script)
		}
	}
}

func TestAvailableDiskBytesWindowsParser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("subprocess fixture uses sh")
	}
	old := commandContext
	t.Cleanup(func() { commandContext = old })
	commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "cat >/dev/null; printf '8192\\n'")
	}
	bytes, err := availableDiskBytesForGOOS(`C:\\data`, "windows")
	if err != nil || bytes != 8192 {
		t.Fatalf("bytes=%d err=%v", bytes, err)
	}
	commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 2")
	}
	if _, err := availableDiskBytesForGOOS(`C:\\data`, "windows"); err == nil {
		t.Fatal("PowerShell failure accepted")
	}
}
