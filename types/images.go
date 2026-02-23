package types

import "time"

type Image struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type StorageConfig struct {
	Path   string `json:"path"`
	RO     bool   `json:"ro"`
	Serial string `json:"serial"`
}

type BootConfig struct {
	KernelPath string `json:"kernel_path"`
	InitrdPath string `json:"initrd_path"`
}
