**--{{ .Name }}**{{ if .Shorthand }} (or **-{{ .Shorthand }}**){{ end }} {{ evaluate .Usage . }}

{{ if .DefValue }}
{{ if eq .DefValue "[]" }}
Default value is an empty list.
{{ else }}
Default value is `{{ .DefValue }}`.
{{ end }}
{{ end }}
