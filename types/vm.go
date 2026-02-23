package types

type VMConfig struct {
	Name    string `json:"name"`
	CPU     int    `json:"cpu"`
	Memory  int    `json:"memory"`
	Storage int    `json:"storage"`
}
