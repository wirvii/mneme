package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const delegationHookFileName = "enforce_delegation.sh"

// WriteDelegationHook writes the embedded enforce_delegation.sh bash hook to
// destDir/enforce_delegation.sh with executable permissions (0755).
//
// The function implements a checksum-based idempotency strategy:
//   - If the destination does not exist, the hook is written and "created" is returned.
//   - If the destination exists and its content is identical to the embedded asset
//     and force is false, nothing is written and "unchanged" is returned.
//   - If the destination exists and its content differs (or force is true), the
//     existing file is backed up as enforce_delegation.sh.bak-YYYYMMDD-HHMMSS and
//     the new content is written. Returns "updated" when content differed, or
//     "reinstalled" when force was true.
//
// destDir is created (with MkdirAll) if it does not already exist.
func WriteDelegationHook(destDir string, force bool) (action string, err error) {
	content, err := DelegationHookContent()
	if err != nil {
		return "", fmt.Errorf("install: write delegation hook: %w", err)
	}

	destPath := filepath.Join(destDir, delegationHookFileName)

	newChecksum := checksumSHA256(content)

	existing, readErr := os.ReadFile(destPath)
	if os.IsNotExist(readErr) {
		// Destination does not exist — write it fresh.
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", fmt.Errorf("install: write delegation hook: mkdir %s: %w", destDir, err)
		}
		if err := os.WriteFile(destPath, content, 0o755); err != nil {
			return "", fmt.Errorf("install: write delegation hook: write: %w", err)
		}
		return "created", nil
	}
	if readErr != nil {
		return "", fmt.Errorf("install: write delegation hook: read existing: %w", readErr)
	}

	existingChecksum := checksumSHA256(existing)
	if existingChecksum == newChecksum && !force {
		return "unchanged", nil
	}

	// Back up the existing file before overwriting.
	ts := time.Now().Format("20060102-150405")
	backupPath := destPath + ".bak-" + ts
	if err := os.Rename(destPath, backupPath); err != nil {
		return "", fmt.Errorf("install: write delegation hook: backup: %w", err)
	}

	if err := os.WriteFile(destPath, content, 0o755); err != nil {
		return "", fmt.Errorf("install: write delegation hook: write: %w", err)
	}

	if force && existingChecksum == newChecksum {
		return "reinstalled", nil
	}
	return "updated", nil
}

// checksumSHA256 returns the lowercase hex-encoded SHA-256 digest of data.
func checksumSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
