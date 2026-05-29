package skill_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/skill"
)

func TestValidate_Pass(t *testing.T) {
	dir := t.TempDir()
	validationDir := filepath.Join(dir, "validation")
	if err := os.MkdirAll(validationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(validationDir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := skill.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("Passed = false, want true; output: %s", result.Output)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestValidate_Fail(t *testing.T) {
	dir := t.TempDir()
	validationDir := filepath.Join(dir, "validation")
	if err := os.MkdirAll(validationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(validationDir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'something went wrong'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := skill.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("Passed = true, want false")
	}
	if result.ExitCode == 0 {
		t.Error("ExitCode = 0, want non-zero")
	}
}

func TestValidate_NoScript(t *testing.T) {
	dir := t.TempDir()

	_, err := skill.Validate(context.Background(), dir)
	if !errors.Is(err, skill.ErrNoValidation) {
		t.Errorf("expected ErrNoValidation, got %v", err)
	}
}

func TestValidate_Timeout(t *testing.T) {
	dir := t.TempDir()
	validationDir := filepath.Join(dir, "validation")
	if err := os.MkdirAll(validationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Script that sleeps longer than our context deadline.
	script := filepath.Join(validationDir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := skill.Validate(ctx, dir)

	// Either the function returns an error (exec killed) or a non-zero result.
	// Both are acceptable; the key is that it does NOT block indefinitely.
	if err == nil && result != nil && result.Passed {
		t.Error("expected timeout to cause failure, but got Passed=true")
	}
}
