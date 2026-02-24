package types

type StorageConfig struct {
	Path   string `json:"path"`
	RO     bool   `json:"ro"`
	Serial string `json:"serial"`
}
