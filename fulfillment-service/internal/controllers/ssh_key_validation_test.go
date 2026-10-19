/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const testSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

var _ = Describe("SSH key Secret validation", func() {
	It("accepts a well-formed SSH public key", func() {
		secret := privatev1.Secret_builder{Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY}.Build()
		Expect(ValidateOpenSSHPublicKeySecret(secret, map[string][]byte{"public_key": []byte(testSSHPublicKey)}, "login")).To(Succeed())
	})

	DescribeTable("rejects unusable Secret data",
		func(secretType privatev1.SecretType, data map[string][]byte, message string) {
			secret := privatev1.Secret_builder{Type: secretType}.Build()
			err := ValidateOpenSSHPublicKeySecret(secret, data, "login")
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring(message))
		},
		Entry("wrong type", privatev1.SecretType_SECRET_TYPE_VALUE,
			map[string][]byte{"public_key": []byte(testSSHPublicKey)}, "expected SECRET_TYPE_SSH_PUBLIC_KEY"),
		Entry("missing public_key", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"other": []byte("value")}, "non-empty 'public_key'"),
		Entry("empty public_key", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"public_key": {}}, "non-empty 'public_key'"),
		Entry("malformed public_key", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"public_key": []byte("not-an-ssh-key")}, "invalid OpenSSH public key"),
		Entry("multiple public keys", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"public_key": []byte(testSSHPublicKey + "\n" + testSSHPublicKey)}, "trailing content"),
	)
})
