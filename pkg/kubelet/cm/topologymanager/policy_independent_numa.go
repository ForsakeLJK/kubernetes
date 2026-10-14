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

package topologymanager

// PolicyIndependentNUMA coordinates resource-specific NUMA placement at pod scope.
const PolicyIndependentNUMA = "independent-numa"

type independentNUMAPolicy struct {
	Policy
}

// NewIndependentNUMAPolicy retains single-node merging for ordinary and legacy
// Pods. Independent resource planning belongs outside the shared Merge contract.
func NewIndependentNUMAPolicy(numaInfo *NUMAInfo, opts PolicyOptions) Policy {
	return &independentNUMAPolicy{Policy: NewSingleNumaNodePolicy(numaInfo, opts)}
}

func (p *independentNUMAPolicy) Name() string {
	return PolicyIndependentNUMA
}
