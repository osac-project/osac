/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package serviceaccounts

func IsAssignableRole(name string) bool {
	switch name {
	case "tenant-automation", "tenant-idp-manager", "tenant-admin", "osac-csi-driver":
		return true
	default:
		return false
	}
}

func IsMachineOnlyRole(name string) bool {
	return name == "tenant-automation" || name == "osac-csi-driver"
}
