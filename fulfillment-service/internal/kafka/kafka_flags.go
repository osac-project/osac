/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
	"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package kafka

import (
	"github.com/spf13/pflag"
)

// AddFlags adds to the given flag set the flags needed to configure the Kafka tool. For example:
//
//	kafka.AddFlags(flags)
//
// Will add the following flags:
//
//	--kafka-properties      Comma separated list of Kafka configuration properties.
//	--kafka-properties-file File or directory containing Kafka configuration properties.
func AddFlags(flags *pflag.FlagSet) {
	_ = flags.String(
		propertiesFlagName,
		"",
		propertiesFlagHelp,
	)
	_ = flags.String(
		propertiesFileFlagName,
		"",
		propertiesFileFlagHelp,
	)
}

// Names of the flags:
const (
	propertiesFlagName     = "kafka-properties"
	propertiesFileFlagName = "kafka-properties-file"
)

const propertiesFlagHelp = `
_PROPERTIES_ - Comma separated list of Kafka configuration properties, for example
'brokers=kafka.example.com:9093,user=my_user,password=my_password'. Valid property names are 'brokers', 'user' and
'password'.
`

const propertiesFileFlagHelp = `
_FILE|DIRECTORY_ - File or directory containing Kafka configuration properties. When pointing to a file the Java
properties format is used, including comments and line continuation. When pointing to a directory the tool scans for
files and treats each file name as a property name and the file contents as the property value.
`
