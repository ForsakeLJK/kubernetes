/*
Copyright 2026 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPodNUMANodeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   *int32
	}{
		{name: "omitted"},
		{name: "zero", id: numaNodePointer(0)},
		{name: "maximum", id: numaNodePointer(63)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &Pod{Spec: PodSpec{NUMANode: tc.id}}
			copy := original.DeepCopy()
			if (copy.Spec.NUMANode == nil) != (tc.id == nil) {
				t.Fatalf("deep copy lost field presence: %+v", copy.Spec.NUMANode)
			}
			jsonData, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(jsonData), "numaNode") != (tc.id != nil) {
				t.Fatalf("JSON field presence incorrect: %s", jsonData)
			}
			var fromJSON Pod
			if err := json.Unmarshal(jsonData, &fromJSON); err != nil {
				t.Fatal(err)
			}
			if (fromJSON.Spec.NUMANode == nil) != (tc.id == nil) {
				t.Fatalf("JSON round trip lost field presence: %+v", fromJSON.Spec.NUMANode)
			}
			protobufData, err := original.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			var fromProtobuf Pod
			if err := fromProtobuf.Unmarshal(protobufData); err != nil {
				t.Fatal(err)
			}
			if (fromProtobuf.Spec.NUMANode == nil) != (tc.id == nil) {
				t.Fatalf("protobuf round trip lost field presence: %+v", fromProtobuf.Spec.NUMANode)
			}
			if tc.id != nil && (*copy.Spec.NUMANode != *tc.id || *fromJSON.Spec.NUMANode != *tc.id || *fromProtobuf.Spec.NUMANode != *tc.id) {
				t.Fatalf("round trip changed NUMA node %d", *tc.id)
			}
		})
	}
}

func numaNodePointer(id int32) *int32 { return &id }
