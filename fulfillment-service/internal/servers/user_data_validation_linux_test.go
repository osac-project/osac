//go:build linux

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isPureXMLNoDTD local file access", func() {
	It("rejects a local-file entity without opening or reading it", func() {
		directory := GinkgoT().TempDir()
		fixture := filepath.Join(directory, "fixture.txt")
		Expect(os.WriteFile(fixture, []byte("local-file-sentinel"), 0o600)).To(Succeed())

		watchFD, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(syscall.Close(watchFD)).To(Succeed())
		})
		_, err = syscall.InotifyAddWatch(watchFD, fixture, syscall.IN_OPEN|syscall.IN_ACCESS)
		Expect(err).NotTo(HaveOccurred())
		Expect(syscall.SetNonblock(watchFD, true)).To(Succeed())

		positive, err := os.ReadFile(fixture)
		Expect(err).NotTo(HaveOccurred())
		Expect(positive).To(Equal([]byte("local-file-sentinel")))
		events, err := readInotifyEvents(watchFD)
		Expect(err).NotTo(HaveOccurred())
		Expect(events & (syscall.IN_OPEN | syscall.IN_ACCESS)).NotTo(BeZero())

		data := []byte(`<!DOCTYPE root [<!ENTITY external SYSTEM "file://` + fixture + `">]><root>&external;</root>`)
		err = isPureXMLNoDTD(data)
		Expect(err).To(MatchError("directive_prohibited"))

		events, err = readInotifyEvents(watchFD)
		Expect(err).NotTo(HaveOccurred())
		Expect(events & (syscall.IN_OPEN | syscall.IN_ACCESS)).To(BeZero())
	})
})

func readInotifyEvents(fd int) (mask uint32, err error) {
	buffer := make([]byte, 4096)
	count, err := syscall.Read(fd, buffer)
	if err != nil {
		if err == syscall.EAGAIN {
			return 0, nil
		}
		return 0, err
	}

	for offset := 0; offset+syscall.SizeofInotifyEvent <= count; {
		event := (*syscall.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
		mask |= event.Mask
		offset += syscall.SizeofInotifyEvent + int(event.Len)
	}
	return mask, nil
}
