package speech

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrInsufficientDisk means setup cannot safely hold staging and final files.
var ErrInsufficientDisk = errors.New("speech: insufficient disk space for managed engine setup")

func availableDiskBytes(path string) (int64, error) {
	return availableDiskBytesForGOOS(path, runtime.GOOS)
}

func availableDiskBytesForGOOS(path, goos string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if goos == "windows" {
		const script = `$p=[Console]::In.ReadLine(); $root=[IO.Path]::GetPathRoot($p).TrimEnd(':\'); (Get-PSDrive -Name $root).Free`
		cmd := commandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.Stdin = strings.NewReader(path + "\n")
		output, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("speech: inspect free disk space: %w", err)
		}
		return parseByteCount(string(output))
	}
	cmd := commandContext(ctx, "df", "-Pk", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("speech: inspect free disk space: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var last string
	for scanner.Scan() {
		last = scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("speech: parse free disk space: %w", err)
	}
	fields := strings.Fields(last)
	if len(fields) < 4 {
		return 0, errors.New("speech: invalid free disk response")
	}
	kilobytes, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, errors.New("speech: invalid free disk response")
	}
	return kilobytes * 1024, nil
}

func parseByteCount(value string) (int64, error) {
	value = strings.TrimSpace(value)
	bytes, err := strconv.ParseInt(value, 10, 64)
	if err != nil || bytes < 0 {
		return 0, errors.New("speech: invalid free disk response")
	}
	return bytes, nil
}
