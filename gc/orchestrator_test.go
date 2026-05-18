package gc

import "testing"

func TestFormatSummary(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]int
		want string
	}{
		{"empty", map[string]int{}, "nothing to collect"},
		{"single", map[string]int{"snapshot": 3}, "snapshot=3"},
		{"sorted", map[string]int{"snapshot": 3, "cloudhypervisor": 1, "oci": 12}, "cloudhypervisor=1 oci=12 snapshot=3"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSummary(tt.in); got != tt.want {
				t.Errorf("formatSummary(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
