package types

import "time"

// SnapshotConfig carries the parameters for creating a snapshot.
// The hypervisor fills ImageBlobIDs (for GC pinning); the CLI adds Name and Description.
type SnapshotConfig struct {
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	ImageBlobIDs map[string]struct{} `json:"image_blob_ids,omitempty"` // blob hex set for GC pinning
}

// Snapshot is the public record for a snapshot.
// Not bound to any specific VM — a snapshot can be restored to any VM.
type Snapshot struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
