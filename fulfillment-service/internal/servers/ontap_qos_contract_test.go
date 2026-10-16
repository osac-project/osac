/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"bytes"
	"encoding/json"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestOntapQosContract(t *testing.T) {
	for _, maxIOPS := range []int64{-1, 0, 5000, 2147483647, 2147483648} {
		config := privatev1.OntapAssociationConfig_builder{MaxIops: maxIOPS}.Build()
		err := protovalidate.Validate(config)
		valid := maxIOPS >= 0 && maxIOPS <= 2147483647
		if (err == nil) != valid {
			t.Errorf("max_iops=%d: validation error=%v, valid=%t", maxIOPS, err, valid)
		}
	}
	association := privatev1.BackendAssociation_builder{
		BackendId: "backend-1",
		Ontap:     privatev1.OntapAssociationConfig_builder{MaxIops: 5000}.Build(),
	}.Build()
	data, err := protojson.Marshal(association)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	if compact.String() != `{"backendId":"backend-1","ontap":{"maxIops":"5000"}}` {
		t.Fatalf("unexpected ProtoJSON contract: %s", data)
	}
	var decoded privatev1.BackendAssociation
	if err := protojson.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(association, &decoded) {
		t.Fatal("association changed during ProtoJSON round trip")
	}
}
