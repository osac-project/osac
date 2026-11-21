//go:build linux

package driver

import (
	"os"

	"golang.org/x/sys/unix"
)

// IsTmpfsMount reports whether path is a tmpfs mount point.
func (systemMounter) IsTmpfsMount(path string) (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	return mountinfoHasTmpfs(f, path)
}

// Unmount lazily detaches the mount at path.
func (systemMounter) Unmount(path string) error {
	return unix.Unmount(path, unix.MNT_DETACH)
}
