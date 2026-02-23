package types

type VMConfig struct {
	Name    string `json:"name"`
	CPU     int    `json:"cpu"`
	Memory  int64  `json:"memory"`
	Storage int64  `json:"storage"`
}
