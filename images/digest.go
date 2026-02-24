package images

import "strings"

// Digest represents a content-addressable digest in "algorithm:hex" format (e.g., "sha256:abcdef...").
type Digest string

// NewDigest creates a Digest from a raw hex string, prefixing "sha256:".
func NewDigest(hex string) Digest {
	return Digest("sha256:" + hex)
}

// Hex returns the hex portion of the digest, stripping the algorithm prefix.
func (d Digest) Hex() string {
	return strings.TrimPrefix(string(d), "sha256:")
}

// String returns the full digest string including the algorithm prefix.
func (d Digest) String() string {
	return string(d)
}
