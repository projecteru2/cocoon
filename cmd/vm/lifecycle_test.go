package vm

import (
	"io"
	"os"
	"testing"
)

func TestSeekToLastNLines(t *testing.T) {
	tests := []struct {
		name string
		body string
		n    int
		want string
	}{
		{name: "no trailing newline", body: "a\nb\nc", n: 2, want: "b\nc"},
		{name: "trailing newline", body: "a\nb\nc\n", n: 2, want: "b\nc\n"},
		{name: "more than available", body: "a\nb", n: 5, want: "a\nb"},
		{name: "one line trailing", body: "a\nb\nc\n", n: 1, want: "c\n"},
		{name: "empty", body: "", n: 3, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "log")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString(tt.body); err != nil {
				t.Fatal(err)
			}
			if err := seekToLastNLines(f, tt.n); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(f)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
