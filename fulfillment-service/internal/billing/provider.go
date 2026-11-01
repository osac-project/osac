/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

// KeyM360 is the only adapter key this service knows.
const KeyM360 = "m360"

// Provider is the deployment's billing state, resolved once at startup. It is the single source of truth for whether
// billing is on: handlers call IsConfigured and never read the environment or a Secret themselves.
type Provider struct {
	key     string
	adapter Adapter
	err     error
}

// NewProvider describes a deployment whose provider is named by key. A non-nil err means the provider is configured
// but its adapter could not be built, for example because the credential Secret is unreadable. An empty key means
// billing is off.
func NewProvider(key string, adapter Adapter, err error) *Provider {
	return &Provider{key: key, adapter: adapter, err: err}
}

// IsConfigured reports whether a provider is configured, whether or not it is currently usable.
func (p *Provider) IsConfigured() bool { return p.key != "" }

// Key is the configured adapter key, such as m360; empty when billing is off.
func (p *Provider) Key() string { return p.key }

// Adapter returns the provider adapter, or the classified error that kept it from being built. Callers check
// IsConfigured first: when billing is off it returns nil, nil.
func (p *Provider) Adapter() (Adapter, error) { return p.adapter, p.err }
