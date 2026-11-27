package driver

import (
	"bufio"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// systemMounter inspects and removes mounts in the node plugin's own mount
// namespace.
type systemMounter struct{}

// mountinfoHasTmpfs reports whether the topmost mount at path in r, a
// /proc/<pid>/mountinfo stream, is a tmpfs.
func mountinfoHasTmpfs(r io.Reader, path string) (bool, error) {
	path = filepath.Clean(path)
	tmpfs := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		// Fields: id parent major:minor root mountpoint options [optional...] - fstype source superoptions
		fields := strings.Fields(scanner.Text())
		sep := slices.Index(fields, "-")
		if sep < 5 || sep+1 >= len(fields) {
			continue
		}
		if unescapeMountinfo(fields[4]) != path {
			continue
		}
		// Later lines are mounted on top of earlier ones.
		tmpfs = fields[sep+1] == "tmpfs"
	}
	return tmpfs, scanner.Err()
}

// unescapeMountinfo decodes the octal escapes (\040 for a space, for
// example) that mountinfo uses in paths.
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
