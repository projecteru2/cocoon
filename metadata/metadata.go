package metadata

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/template"
)

const cidataLabel = "CIDATA"

// Config holds the inputs for generating cloud-init NoCloud metadata.
type Config struct {
	InstanceID   string
	Hostname     string
	RootPassword string
}

var tmplFuncs = template.FuncMap{
	// yamlQuote escapes single quotes for YAML single-quoted strings.
	"yamlQuote": func(s string) string {
		return strings.ReplaceAll(s, "'", "''")
	},
}

var metaDataTmpl = template.Must(template.New("meta-data").Parse(
	"instance-id: {{.InstanceID}}\nlocal-hostname: {{.Hostname}}\n"))

var userDataTmpl = template.Must(template.New("user-data").Funcs(tmplFuncs).Parse(`#cloud-config
{{- if .RootPassword}}
chpasswd:
  expire: false
  list:
    - 'root:{{yamlQuote .RootPassword}}'
ssh_pwauth: true
disable_root: false
{{- end}}
`))

// Generate streams a cloud-init NoCloud cidata disk image (FAT12) to w.
func Generate(w io.Writer, cfg *Config) error {
	files := make(map[string][]byte, 2) //nolint:mnd

	var buf bytes.Buffer
	if err := metaDataTmpl.Execute(&buf, cfg); err != nil {
		return fmt.Errorf("render meta-data: %w", err)
	}
	files["meta-data"] = bytes.Clone(buf.Bytes())

	buf.Reset()
	if err := userDataTmpl.Execute(&buf, cfg); err != nil {
		return fmt.Errorf("render user-data: %w", err)
	}
	files["user-data"] = buf.Bytes()

	return CreateFAT12(w, cidataLabel, files)
}
