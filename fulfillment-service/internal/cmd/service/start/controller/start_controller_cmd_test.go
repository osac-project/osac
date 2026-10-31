//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package controller

import (
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller command")
}

var _ = Describe("Controller event filters", func() {
	It("compiles and evaluates the FabricDomain event filter", func() {
		env, err := cel.NewEnv(
			cel.Types(&privatev1.Event{}),
			cel.Variable("event", cel.ObjectType("osac.private.v1.Event")),
			cel.Constant("EVENT_TYPE_OBJECT_UPDATED", cel.IntType, types.Int(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED)),
		)
		Expect(err).To(Succeed())
		ast, issues := env.Compile(fabricDomainEventFilter)
		if issues != nil {
			Expect(issues.Err()).To(Succeed())
		}
		program, err := env.Program(ast)
		Expect(err).To(Succeed())

		cases := []struct {
			name  string
			event *privatev1.Event
			want  bool
		}{
			{
				name: "unrelated virtual network update",
				event: privatev1.Event_builder{
					Type:           privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					VirtualNetwork: privatev1.VirtualNetwork_builder{}.Build(),
				}.Build(),
			},
			{
				name: "virtual network hub assignment",
				want: true,
				event: privatev1.Event_builder{
					Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					VirtualNetwork: privatev1.VirtualNetwork_builder{
						Status: privatev1.VirtualNetworkStatus_builder{Hub: "hub-a"}.Build(),
					}.Build(),
				}.Build(),
			},
			{
				name: "virtual network deletion request",
				want: true,
				event: privatev1.Event_builder{
					Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					VirtualNetwork: privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{DeletionTimestamp: timestamppb.Now()}.Build(),
					}.Build(),
				}.Build(),
			},
			{
				name:  "unrelated hub creation",
				event: privatev1.Event_builder{Hub: privatev1.Hub_builder{}.Build()}.Build(),
			},
			{
				name:  "fabric domain event",
				want:  true,
				event: privatev1.Event_builder{FabricDomain: privatev1.FabricDomain_builder{}.Build()}.Build(),
			},
		}
		for _, tc := range cases {
			By(tc.name)
			value, _, evalErr := program.Eval(map[string]any{"event": tc.event})
			Expect(evalErr).To(Succeed())
			got, ok := value.(types.Bool)
			Expect(ok).To(BeTrue())
			Expect(bool(got)).To(Equal(tc.want))
		}
	})

	It("matches service-account events and ignores other resource events", func() {
		env, err := cel.NewEnv(
			cel.Types(&privatev1.Event{}),
			cel.Variable("event", cel.ObjectType("osac.private.v1.Event")),
		)
		Expect(err).To(Succeed())
		ast, issues := env.Compile(serviceAccountEventFilter)
		if issues != nil {
			Expect(issues.Err()).To(Succeed())
		}
		program, err := env.Program(ast)
		Expect(err).To(Succeed())
		for _, event := range []*privatev1.Event{
			privatev1.Event_builder{ServiceAccount: privatev1.ServiceAccount_builder{}.Build()}.Build(),
			privatev1.Event_builder{Role: privatev1.Role_builder{}.Build()}.Build(),
		} {
			value, _, evalErr := program.Eval(map[string]any{"event": event})
			Expect(evalErr).To(Succeed())
			Expect(value).To(Equal(types.Bool(event.HasServiceAccount())))
		}
	})
})
