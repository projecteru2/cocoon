package images

import godigest "github.com/opencontainers/go-digest"

// Digest represents a content-addressable digest in "algorithm:hex" format
// (e.g., "sha256:abcdef..."). Backed by opencontainers/go-digest.
type Digest string

// NewDigest creates a Digest from a raw hex string, prefixing "sha256:".
func NewDigest(hex string) Digest {
	return Digest(godigest.NewDigestFromEncoded(godigest.SHA256, hex))
}

// Hex returns the hex portion of the digest, stripping the algorithm prefix.
func (d Digest) Hex() string {
	return godigest.Digest(d).Encoded()
}

// String returns the full digest string including the algorithm prefix.
func (d Digest) String() string {
	return string(d)
}
