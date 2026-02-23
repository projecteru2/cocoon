package types

import "time"

type Storage struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Hex       string    `json:"hex"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type StorageConfig struct {
	Path   string `json:"path"`
	RO     bool   `json:"ro"`
	Serial string `json:"serial"`
}
