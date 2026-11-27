//go:build !linux

package driver

import "errors"

// IsTmpfsMount reports false: mount inspection is only supported on Linux.
func (systemMounter) IsTmpfsMount(string) (bool, error) {
	return false, nil
}

// Unmount is only supported on Linux.
func (systemMounter) Unmount(string) error {
	return errors.New("unmount is only supported on Linux")
}
