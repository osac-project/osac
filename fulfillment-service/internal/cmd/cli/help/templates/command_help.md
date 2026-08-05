# {{ .Name }}

{{ if .Long }}{{ evaluate .Long . }}{{ else }}{{ evaluate .Short . }}{{ end }}

## Usage

```shell
{{ .UseLine }}
```


{{ if .HasAvailableSubCommands }}
## Commands
{{ range .Commands }}
{{ if .IsAvailableCommand }}
* **{{ .Name }}** - {{ .Short }}.
{{ end }}
{{ end }}
{{ end }}

{{ $flags := flags .LocalFlags }}
{{ if $flags }}
## Flags
{{ range $flags }}
{{ execute "flag_help.md" . }}
{{ end }}
{{ end }}

{{ if .HasAvailableInheritedFlags }}
## Global flags
{{ if not .Parent }}
{{ range flags .InheritedFlags }}
{{ execute "flag_help.md" . }}
{{ end }}
{{ else }}
For details about global flags, including logging and configuration, use the `{{ binary }} help` command.
{{ end }}
{{ end }}
