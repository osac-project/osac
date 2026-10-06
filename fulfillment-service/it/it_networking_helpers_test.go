/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func waitForNetworkClassReady(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	id string,
) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		object := response.GetObject()
		g.Expect(object.GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY),
			"NetworkClass %q did not become ready: %s",
			id,
			object.GetStatus().GetMessage(),
		)
	}, time.Minute, time.Second).Should(Succeed())
}
