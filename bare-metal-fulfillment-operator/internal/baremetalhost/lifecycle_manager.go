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

package baremetalhost

import "context"

// BMHLifecycleManager exposes the BareMetalHost operations used by inventory
// backends. Manager implements this interface.
//
//go:generate mockgen -destination=bmh_lifecycle_manager_mock.go -package=baremetalhost . BMHLifecycleManager
type BMHLifecycleManager interface {
	CreateBMH(ctx context.Context, params CreateParams) error
	DeleteBMH(ctx context.Context, name string) error
	BMHExists(ctx context.Context, name string) (bool, error)
	IsBMHReady(ctx context.Context, name string) (bool, error)
	EnsureBMCSecret(ctx context.Context, name, username, password string) error
	DeleteBMCSecret(ctx context.Context, name string) error
	GetHardwareNICs(ctx context.Context, name string) ([]string, error)
	Namespace() string
}

var _ BMHLifecycleManager = (*Manager)(nil)
