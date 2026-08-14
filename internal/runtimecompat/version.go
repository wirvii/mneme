// Package runtimecompat detects whether an installed agent CLI provides the
// native hooks and project-agent formats required by mneme.
package runtimecompat

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Minimum versions are the oldest releases verified for SPEC-123's native
// subagent, hooks and MCP configuration contracts.
const (
	MinimumClaudeCode = "2.1.232"
	MinimumCodex      = "0.147.0"
)

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Status describes the local runtime compatibility result.
type Status struct {
	Slug      string
	Command   string
	Installed bool
	Version   string
	Minimum   string
	Supported bool
}

// Detect executes the runtime's version command. A missing CLI is a
// reportable state rather than an error so project assets can still be
// generated and statically validated for that runtime.
func Detect(slug string) (Status, error) {
	command, minimum, err := contract(slug)
	if err != nil {
		return Status{}, err
	}
	status := Status{Slug: slug, Command: command, Minimum: minimum}
	if _, err := exec.LookPath(command); err != nil {
		return status, nil
	}
	out, err := exec.Command(command, "--version").CombinedOutput()
	if err != nil {
		return status, fmt.Errorf("runtime compatibility: %s --version: %w", command, err)
	}
	version, err := ParseVersion(string(out))
	if err != nil {
		return status, fmt.Errorf("runtime compatibility: %s: %w", command, err)
	}
	status.Installed = true
	status.Version = version
	status.Supported = Compare(version, minimum) >= 0
	return status, nil
}

// ParseVersion extracts a semantic three-component version from CLI output.
func ParseVersion(output string) (string, error) {
	version := versionPattern.FindString(output)
	if version == "" {
		return "", fmt.Errorf("version is not parseable")
	}
	return version, nil
}

// Compare returns -1, 0 or 1 when left is lower, equal or higher than right.
func Compare(left, right string) int {
	l := versionPattern.FindStringSubmatch(left)
	r := versionPattern.FindStringSubmatch(right)
	for i := 0; i < 3; i++ {
		lv, _ := strconv.Atoi(component(l, i))
		rv, _ := strconv.Atoi(component(r, i))
		if lv < rv {
			return -1
		}
		if lv > rv {
			return 1
		}
	}
	return 0
}

func component(match []string, index int) string {
	if len(match) == 0 {
		return "0"
	}
	parts := strings.Split(match[0], ".")
	if index >= len(parts) {
		return "0"
	}
	return parts[index]
}

func contract(slug string) (string, string, error) {
	switch slug {
	case "claude-code":
		return "claude", MinimumClaudeCode, nil
	case "codex":
		return "codex", MinimumCodex, nil
	default:
		return "", "", fmt.Errorf("runtime compatibility: unsupported agent %q", slug)
	}
}
