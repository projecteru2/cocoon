package oci

import (
	"fmt"
	"strings"
)

// normalizeRef ensures the ref has a tag; appends :latest if missing.
func normalizeRef(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty image reference")
	}
	// If no tag/digest specified, append :latest.
	if !strings.Contains(ref, ":") && !strings.Contains(ref, "@") {
		ref += ":latest"
	}
	return ref, nil
}
