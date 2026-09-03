/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package services

import (
	"fmt"

	"github.com/spf13/pflag"
)

type Flags struct {
	CaaS  bool
	VMaaS bool
	BMaaS bool
	MaaS  bool
}

func RegisterFlags(flags *pflag.FlagSet) *Flags {
	f := &Flags{}
	flags.BoolVar(&f.CaaS, "enable-caas", false,
		"Enable the CaaS (Container as a Service) endpoints.")
	flags.BoolVar(&f.VMaaS, "enable-vmaas", false,
		"Enable the VMaaS (Virtual Machine as a Service) endpoints.")
	flags.BoolVar(&f.BMaaS, "enable-bmaas", false,
		"Enable the BMaaS (Bare Metal as a Service) endpoints.")
	flags.BoolVar(&f.MaaS, "enable-maas", false,
		"Enable the MaaS (Metal as a Service) endpoints.")
	return f
}

func (f *Flags) EnableAllIfNoneSet() {
	if !f.CaaS && !f.VMaaS && !f.BMaaS && !f.MaaS {
		f.CaaS = true
		f.VMaaS = true
		f.BMaaS = true
		f.MaaS = true
	}
}

func (f *Flags) Validate() error {
	if f.CaaS && !f.VMaaS && !f.BMaaS {
		return fmt.Errorf("CaaS requires at least one of VMaaS or BMaaS")
	}
	if f.MaaS && !f.CaaS {
		return fmt.Errorf("MaaS requires CaaS")
	}
	return nil
}

func (f *Flags) EnabledServices() []string {
	var result []string
	if f.CaaS {
		result = append(result, "caas")
	}
	if f.VMaaS {
		result = append(result, "vmaas")
	}
	if f.BMaaS {
		result = append(result, "bmaas")
	}
	if f.MaaS {
		result = append(result, "maas")
	}
	if result == nil {
		return []string{}
	}
	return result
}
