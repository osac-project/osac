/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logging

import "github.com/spf13/pflag"

// AddFlags adds the flags related to logging to the given flag set.
func AddFlags(set *pflag.FlagSet) {
	_ = set.String(
		levelFlagName,
		"error",
		logFlagHelp,
	)
	_ = set.String(
		fileFlagName,
		"stdout",
		fileFlagHelp,
	)
	_ = set.StringArray(
		fieldFlagName,
		[]string{},
		fieldFlagHelp,
	)
	_ = set.StringSlice(
		fieldsFlagName,
		[]string{},
		fieldsFlagHelp,
	)
	_ = set.Bool(
		headersFlagName,
		false,
		headersFlagHelp,
	)
	_ = set.Bool(
		bodiesFlagName,
		false,
		bodiesFlagHelp,
	)
	_ = set.Bool(
		redactFlagName,
		true,
		redactFlagHelp,
	)
}

// Names of the flags:
const (
	levelFlagName   = "log-level"
	fileFlagName    = "log-file"
	fieldFlagName   = "log-field"
	fieldsFlagName  = "log-fields"
	headersFlagName = "log-headers"
	bodiesFlagName  = "log-bodies"
	redactFlagName  = "log-redact"
)

const logFlagHelp = `
_LEVEL_ - Log level. Possible values are 'debug', 'info', 'warn' and 'error'.
`

const fileFlagHelp = `
_FILE_ - Log file. The value can also be {{ bt }}stdout{{ bt }} or
{{ bt }}stderr{{ bt }} and then the log will be written to the
standard output or error stream of the process.
`
const fieldFlagHelp = `
_%LETTER|FIELD=VALUE_ - Field to add to all log messages. The value can be a
percent sign followed by one of the letters that indicate a special value, or a
field name followed by an equals sign and the field value. For example
{{ bt }}%p{{ bt }} results in a field named 'pid' containing the identifier
 of the process, and {{ bt }}my-field=my-value{{ bt }} results in adding a
 field named {{ bt }}my-field{{ bt }} with value {{ bt }}my-value{{ bt }}.

The following percent letters are supported:

- %p - The process identifier.
)
This is intended for situations where multiple processes are writing to the
same log file, and you want to be able to identify the messages from each
process.

It can be used multiple times to add multiple fields.
`

const fieldsFlagHelp = `
_%LETTER|FIELD=VALUE..._ - Comma separated list of fields to add to all log
messages. See the {{ bt }}--log-field{{ bt }} option for details of allowed values.
Note that this doesn't allow values containing commas, use the {{ bt }}--log-field{{ bt }}
option if you need that.
`

const headersFlagHelp = `
_[BOOLEAN]_ - Include gRPC/HTTP headers in log messages.
`

const bodiesFlagHelp = `
_[BOOLEAN]_ - Include details of gRPC/HTTP request and response bodies in log messages.

Currently only the size of the body is written, not the complete body.
`

const redactFlagHelp = `
_[BOOLEAN]_ - Enables or disables redaction of security-sensitive data from the log.
`
