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

package memorymanager

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/kubernetes/test/utils/ktesting"
)

func TestNUMAPlacementRefusesRestoredMemoryAssignment(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(2)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("restored-pod")}, Spec: v1.PodSpec{
		NUMANode:   &node,
		Containers: []v1.Container{{Name: "app"}},
	}}
	memoryState := state.NewMemoryState(logger)
	blocks := []state.Block{{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 1024}}
	memoryState.SetMemoryBlocks(string(pod.UID), "app", blocks)
	m := &manager{state: memoryState}
	container := &pod.Spec.Containers[0]
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("restored assignment was not refused: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err == nil {
		t.Fatal("restored assignment could reach runtime configuration")
	}
	if actual := memoryState.GetMemoryBlocks(string(pod.UID), container.Name); len(actual) != 1 || actual[0].NUMAAffinity[0] != 2 {
		t.Fatalf("restored memory assignment was modified: %+v", actual)
	}
}
