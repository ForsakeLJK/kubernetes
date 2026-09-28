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

package v1_test

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/apis/core"
	corev1 "k8s.io/kubernetes/pkg/apis/core/v1"
)

func TestNUMANodePodSpecConversion(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   *int32
	}{
		{name: "omitted"},
		{name: "zero", id: numaConversionID(0)},
		{name: "maximum", id: numaConversionID(63)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			internal := &core.PodSpec{}
			if err := corev1.Convert_v1_PodSpec_To_core_PodSpec(&v1.PodSpec{NUMANode: tc.id}, internal, nil); err != nil {
				t.Fatal(err)
			}
			result := &v1.PodSpec{}
			if err := corev1.Convert_core_PodSpec_To_v1_PodSpec(internal, result, nil); err != nil {
				t.Fatal(err)
			}
			if (result.NUMANode == nil) != (tc.id == nil) || (tc.id != nil && *result.NUMANode != *tc.id) {
				t.Fatalf("conversion lost NUMA node presence or value: %+v", result.NUMANode)
			}
		})
	}
}

func numaConversionID(id int32) *int32 { return &id }
