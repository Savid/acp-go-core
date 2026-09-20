package process

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScratchDir creates one ephemeral directory for one purpose under the scratch
// parent, which is the system temp directory when empty. The parent is
// created 0700 when missing, and the name carries the acp-go-<vendor>-<purpose>-
// prefix so a host can sweep orphans.
func ScratchDir(parent, vendor, purpose string) (string, error) {
	parent, err := scratchParent(parent)
	if err != nil {
		return "", err
	}

	return os.MkdirTemp(parent, scratchPrefix(vendor, purpose))
}

// ScratchPath names the directory for one purpose and one name under the
// scratch parent without creating it, so a sibling can address content the
// same way across launches.
func ScratchPath(parent, vendor, purpose, name string) (string, error) {
	parent, err := scratchParent(parent)
	if err != nil {
		return "", err
	}

	return filepath.Join(parent, scratchPrefix(vendor, purpose)+name), nil
}

func scratchParent(parent string) (string, error) {
	if parent == "" {
		parent = os.TempDir()
	}

	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create scratch parent: %w", err)
	}

	return parent, nil
}

func scratchPrefix(vendor, purpose string) string {
	return "acp-go-" + vendor + "-" + purpose + "-"
}
