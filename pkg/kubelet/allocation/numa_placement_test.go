/*
Copyright The Kubernetes Authors.

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

package allocation

import (
	"context"
	v1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/utils/ptr"
	"strings"
	"testing"
)

func TestNUMAPlacementRefusesKubeletResize(t *testing.T) {
	for _, spec := range []v1.PodSpec{{NUMANode: ptr.To[int32](0)}, {CPUNUMA: ptr.To[int32](0), MemNUMA: ptr.To[int32](3)}} {
		result := NewPodResizesAdmitHandler(nil, nil, nil).Admit(context.Background(), &lifecycle.PodAdmitAttributes{Pod: &v1.Pod{Spec: spec}, Operation: lifecycle.ResizeOperation})
		if result.Admit || !strings.Contains(result.Message, "NUMA placement") {
			t.Fatalf("resize = %+v", result)
		}
	}
}
