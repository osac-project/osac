/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
)

// Execute runs the CLI command and adds endpoint guidance to authentication and
// authorization failures returned by API commands.
func Execute(root *cobra.Command) error {
	cmd, err := root.ExecuteC()
	if err == nil {
		return nil
	}
	return addAuthHint(cmd, err)
}

func addAuthHint(cmd *cobra.Command, err error) error {
	var hint string
	switch status.Code(err) {
	case codes.Unauthenticated:
		hint = "Authentication failed. Check that your token is valid and its issuer is reachable."
	case codes.PermissionDenied:
		hint = "The token was accepted, but this operation is not authorized. Check tenant membership " +
			"and roles for tenant requests, or verify that your service account is authorized for " +
			"platform operations."
	default:
		return err
	}

	hint += " Match the API to your persona and operation:\n" +
		"  Tenant: osac login https://fulfillment-api-<namespace>.<cluster-domain> --flow device\n" +
		"  Platform admin: osac login https://fulfillment-internal-api-<namespace>.<cluster-domain> --private \\\n" +
		"    --token-script '<authorized service-account token command>'"

	if cmd != nil && cmd.Context() != nil {
		if settings := config.TrySettingsFromContext(cmd.Context()); settings != nil {
			if settings.Private() {
				hint += "\nThis CLI profile has private API mode enabled. For tenant access, log in to " +
					"the public API address without --private."
			} else {
				hint += "\nThis CLI profile does not have private API mode enabled. For platform operations, " +
					"log in to the private API address with --private."
			}
		}
	}

	hint += "\nSee https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/AUTH.md."
	return fmt.Errorf("%w\nHint: %s", err, hint)
}
