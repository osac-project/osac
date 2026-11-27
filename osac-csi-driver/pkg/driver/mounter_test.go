package driver

import (
	"strings"
	"testing"
)

func TestMountinfoHasTmpfs(t *testing.T) {
	const mountinfo = `22 1 0:21 / / rw,relatime shared:1 - xfs /dev/vda4 rw
30 22 0:25 / /var/lib/kubelet/pods/p/volumes/v/mount rw shared:5 - tmpfs tmpfs rw,size=1k
31 22 0:26 / /var/lib/kubelet/pods/p/volumes/nfs/mount rw - tmpfs tmpfs rw
32 31 0:27 / /var/lib/kubelet/pods/p/volumes/nfs/mount rw - nfs4 pool.example:/export rw
33 22 0:28 / /var/lib/kubelet/with\040space rw - tmpfs tmpfs rw
`
	tests := map[string]bool{
		"/var/lib/kubelet/pods/p/volumes/v/mount":   true,
		"/var/lib/kubelet/pods/p/volumes/v/mount/":  true,
		"/var/lib/kubelet/pods/p/volumes/nfs/mount": false,
		"/var/lib/kubelet/with space":               true,
		"/var/lib/kubelet/pods/p/volumes/absent":    false,
		"/":                                         false,
	}
	for path, want := range tests {
		got, err := mountinfoHasTmpfs(strings.NewReader(mountinfo), path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got != want {
			t.Errorf("mountinfoHasTmpfs(%q) = %v, want %v", path, got, want)
		}
	}
}
