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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("buildStorageClassDefaultCheck", func() {
	entry := matrixEntry{ID: "default-storageclass", Kind: "storageclass-default", Severity: "required", Category: "resource"}

	It("is a Required check", func() {
		check, err := buildStorageClassDefaultCheck(entry)
		Expect(err).NotTo(HaveOccurred())
		Expect(check.Severity).To(Equal(Required))
	})

	It("passes and names the class when a default StorageClass exists", func() {
		check, _ := buildStorageClassDefaultCheck(entry)
		clients := newFakeClients([]runtime.Object{
			&storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "standard",
					Annotations: map[string]string{defaultStorageClassAnnotation: "true"},
				},
			},
		}, nil)

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
		Expect(message).To(ContainSubstring("standard"))
	})

	It("fails when no StorageClass exists at all", func() {
		check, _ := buildStorageClassDefaultCheck(entry)
		clients := newFakeClients(nil, nil)

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("no default StorageClass"))
	})

	It("fails when StorageClasses exist but none is marked default", func() {
		check, _ := buildStorageClassDefaultCheck(entry)
		clients := newFakeClients([]runtime.Object{
			&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "fast"}},
			&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "slow"}},
		}, nil)

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("no default StorageClass"))
	})

	It("treats any non-'true' annotation value as not-default", func() {
		check, _ := buildStorageClassDefaultCheck(entry)
		clients := newFakeClients([]runtime.Object{
			&storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "standard",
					Annotations: map[string]string{defaultStorageClassAnnotation: "false"},
				},
			},
		}, nil)

		status, _ := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
	})
})
