/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"github.com/spf13/pflag"
)

// AddFlags adds to the given flag set the flags needed to configure the database tool. For example:
//
//	database.AddFlags(flags)
//
// Will add the following flags:
//
//	--db-url      Database connection URL.
//	--db-url-file File or directory containing the database connection URL or settings.
func AddFlags(flags *pflag.FlagSet) {
	_ = flags.String(
		urlFlagName,
		"",
		urlFlagHelp,
	)
	_ = flags.String(
		urlFileFlagName,
		"",
		urlFileFlagHelp,
	)
}

// Names of the flags:
const (
	urlFlagName     = "db-url"
	urlFileFlagName = "db-url-file"
)

const urlFlagHelp = `
_URL_ - Database connection URL.
`

const urlFileFlagHelp = `
_FILE|DIRECTORY_ - File or directory containing the database connection
settings. When pointing to a file the URL is read from it. When pointing to a
directory the tool scans for files named after connection parameters and builds
the URL from them.
`
