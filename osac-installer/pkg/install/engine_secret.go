/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretRef identifies a Secret that must exist before OSAC installs, e.g.
// the AAP license manifest or a database connection Secret. Unlike the
// prerequisites matrix (data/prerequisites.yaml), these aren't fixed facts
// about OSAC's architecture: their names are whatever the deployer
// configured in their own Helm values (service.database.connection[].
// secret.name, aap.configAsCode.manifestSecret). SecretChecks therefore
// takes them as an argument from the caller -- a CLI flag for a human, or
// the Helm hook's own already-rendered values -- rather than the matrix
// hardcoding a name that might not match what was actually configured.
type SecretRef struct {
	Namespace string
	Name      string
	// Keys, if non-empty, are Secret data keys that must also be present.
	Keys []string
}

// SecretChecks builds one Required-severity Check per ref, confirming the
// Secret exists and, if ref.Keys is set, that every key is present.
func SecretChecks(refs []SecretRef) []Check {
	checks := make([]Check, 0, len(refs))
	for _, ref := range refs {
		checks = append(checks, buildSecretExistsCheck(ref))
	}
	return checks
}

func buildSecretExistsCheck(ref SecretRef) Check {
	return Check{
		Name:        fmt.Sprintf("secret-%s-%s", ref.Namespace, ref.Name),
		Description: fmt.Sprintf("Secret %s/%s exists", ref.Namespace, ref.Name),
		Severity:    Required,
		Category:    ResourceCategory,
		Run: func(ctx context.Context, clients *Clients) (Status, string) {
			secret, err := clients.Typed.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			switch {
			case apierrors.IsNotFound(err):
				return Failed, fmt.Sprintf("Secret %s/%s not found", ref.Namespace, ref.Name)
			case err != nil:
				return Failed, fmt.Sprintf("failed to check for Secret %s/%s: %v", ref.Namespace, ref.Name, err)
			}
			if len(ref.Keys) == 0 {
				return Pass, fmt.Sprintf("Secret %s/%s found", ref.Namespace, ref.Name)
			}
			var missing []string
			for _, key := range ref.Keys {
				if _, ok := secret.Data[key]; !ok {
					missing = append(missing, key)
				}
			}
			if len(missing) > 0 {
				return Failed, fmt.Sprintf("Secret %s/%s is missing key(s): %s", ref.Namespace, ref.Name, strings.Join(missing, ", "))
			}
			return Pass, fmt.Sprintf("Secret %s/%s found with required keys", ref.Namespace, ref.Name)
		},
	}
}
