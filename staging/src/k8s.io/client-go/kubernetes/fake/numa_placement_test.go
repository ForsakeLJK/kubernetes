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

package fake

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreapply "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestPodNUMANodeClientOperations(t *testing.T) {
	ctx := context.Background()
	client := NewClientset()
	pods := client.CoreV1().Pods("default")

	omitted, err := pods.Create(ctx, &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "omitted", Namespace: "default"}}, metav1.CreateOptions{})
	if err != nil || omitted.Spec.NUMANode != nil {
		t.Fatalf("create omitted NUMA node: pod=%+v, err=%v", omitted, err)
	}

	zero, err := pods.Apply(ctx, coreapply.Pod("zero", "default").WithSpec(coreapply.PodSpec().WithNUMANode(0)), metav1.ApplyOptions{FieldManager: "numa-test"})
	if err != nil || zero.Spec.NUMANode == nil || *zero.Spec.NUMANode != 0 {
		t.Fatalf("apply NUMA node zero: pod=%+v, err=%v", zero, err)
	}
	zero, err = pods.Get(ctx, "zero", metav1.GetOptions{})
	if err != nil || zero.Spec.NUMANode == nil || *zero.Spec.NUMANode != 0 {
		t.Fatalf("get NUMA node zero: pod=%+v, err=%v", zero, err)
	}
	zero.Labels = map[string]string{"updated": "true"}
	updated, err := pods.Update(ctx, zero, metav1.UpdateOptions{})
	if err != nil || updated.Spec.NUMANode == nil || *updated.Spec.NUMANode != 0 {
		t.Fatalf("update NUMA node zero: pod=%+v, err=%v", updated, err)
	}
	omitted, err = pods.Get(ctx, "omitted", metav1.GetOptions{})
	if err != nil || omitted.Spec.NUMANode != nil {
		t.Fatalf("get omitted NUMA node: pod=%+v, err=%v", omitted, err)
	}
}
