package types

import "time"

// SnapshotConfig carries the parameters for creating a snapshot.
// The hypervisor fills Image and ImageBlobIDs; the CLI adds Name and Description.
type SnapshotConfig struct {
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Image        string              `json:"image,omitempty"`          // source image reference (e.g. "ubuntu:22.04")
	ImageBlobIDs map[string]struct{} `json:"image_blob_ids,omitempty"` // blob hex set for GC pinning
}

// Snapshot is the public record for a snapshot.
// Not bound to any specific VM — a snapshot can be restored to any VM.
type Snapshot struct {
	SnapshotConfig
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}
