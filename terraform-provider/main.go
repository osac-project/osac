//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

// terraform-provider-osac is the Terraform (and OpenTofu) provider for the OSAC fulfillment service. Run `osac login`
// once to create the session, then use this provider to manage bare metal instances.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	osacprovider "github.com/osac-project/osac/terraform-provider/internal/provider"
)

// version is set at build time with -ldflags="-X main.version=...".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), func() provider.Provider {
		return osacprovider.New(version)
	}, providerserver.ServeOpts{
		Address: "registry.opentofu.org/osac-project/osac",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
