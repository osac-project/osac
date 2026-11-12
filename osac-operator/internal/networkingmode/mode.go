/*
Copyright 2026.

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

// Package networkingmode resolves the installation-wide provider networking mode
// from the deployment's NetworkClass.
package networkingmode

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// ErrUnresolved reports that the installation networking mode cannot be read
// yet: there is no NetworkClass, its provisioning mode hasn't been backfilled,
// more than one NetworkClass exists, or the API is unavailable. It is
// transient. Callers must not treat it as disabled and must take no provider
// action.
var ErrUnresolved = errors.New("the installation networking mode is not resolved")

// MismatchError reports a connection deployment whose configured value differs
// from the mode recorded on the NetworkClass. The NetworkClass is the authority;
// a mismatch means a rollout changed the Helm value without reinstalling.
type MismatchError struct {
	Configured bool
	Accepted   bool
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("global.networking.provisioningEnabled is %t but the NetworkClass records %t: "+
		"global.networking.provisioningEnabled cannot change after installation; reinstall to change it",
		e.Configured, e.Accepted)
}

// Resolve returns the provider networking mode of the deployment's NetworkClass
// in the given namespace. An unset mode is ErrUnresolved, never disabled.
func Resolve(ctx context.Context, reader client.Reader, namespace string) (bool, error) {
	list := &v1alpha1.NetworkClassList{}
	if err := reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("%w: listing NetworkClasses: %v", ErrUnresolved, err)
	}
	var active []*v1alpha1.NetworkClass
	for index := range list.Items {
		if list.Items[index].DeletionTimestamp == nil {
			active = append(active, &list.Items[index])
		}
	}
	switch len(active) {
	case 0:
		return false, fmt.Errorf("%w: no NetworkClass exists yet", ErrUnresolved)
	case 1:
	default:
		return false, fmt.Errorf("%w: more than one NetworkClass exists", ErrUnresolved)
	}
	class := active[0]
	if class.Spec.ProvisioningEnabled == nil {
		return false, fmt.Errorf("%w: NetworkClass %s has no provisioning mode", ErrUnresolved, class.Name)
	}
	return *class.Spec.ProvisioningEnabled, nil
}
