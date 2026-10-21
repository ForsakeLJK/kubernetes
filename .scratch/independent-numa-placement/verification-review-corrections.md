# Post-milestone Spec review corrections

Date: 2026-10-19. Baseline: `b76f73a6f478ef3ed63cdf58bcb295dfcec1013b`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.
Review range: `numa_support...HEAD`, the 21 independent-placement commits.

A two-axis review (Standards, Spec) ran after ticket 10. This record covers the
five Spec findings classed "implemented but looks wrong" and the Standards
finding on actionable error messages. Spec scope-creep findings and the other
Standards findings remain open.

## Corrections

| Finding | Spec line | Owning ticket | Change |
| --- | --- | --- | --- |
| Distance ranking refused when any column of the CPU row was malformed, including unrelated nodes. | "If relevant distances cannot establish the closest feasible candidate, refuse admission" | [04](issues/04-run-memory-only-requests.md), inherited by 05 | `cpuToMemoryDistance` still validates row shape and node IDs, then checks only the local anchor and the compared column. |
| `prefer-closest-numa-nodes` lost its missing-distance startup refusal under independent-numa. | "existing policy option behavior remains intact" | [04](issues/04-run-memory-only-requests.md) | `newNUMAInfo` applies the option's check under every policy. Independent-numa still loads without the option. |
| A CPU target whose CPUs were all reserved got a generic shortage error. | "A CPU target must contain eligible CPUs." | [03](issues/03-run-explicit-cpu-memory-pairs.md) | CPU Manager's `checkNUMAPlacement` refuses an explicit CPU target with no allocatable CPUs, naming the reserved set. The kubelet handler's CPU-less check is unchanged. |
| Common-node alignment was under-counted for recovered Pods with completed init containers. | "Global common-node alignment metrics must count only actual common-node placement" | [06](issues/06-enforce-pod-wide-placement.md) | `hasCommonNUMAPlacement` skips the completed sequential init containers that admission also skips. |
| Range and Windows API errors for the three NUMA fields came out in map order. | Determinism of validation (review finding; no spec line) | [03](issues/03-run-explicit-cpu-memory-pairs.md) | `podNUMAFields` gives `numaNode`, `cpuNUMA`, `memNUMA` order to both checks. |

## Standards correction: actionable error messages

`AGENTS.md`: "Error messages: actionable and specific." When one target was
automatic, several refusals printed the unresolved sentinel `-1`. The
review-cited case was the resize refusal, "NUMA node -1 does not support
in-place resource resize". The same problem was in the CPU/memory
`ValidateNUMAPlacement` missing-allocation errors, the memory "no memory
blocks" conflict, both runtime-start checks in
`internal_container_lifecycle_linux.go`, and `CheckPodNUMANodes` "(target -1)".
For split Pods, the kubelet admit handler printed only one of the two targets.

`NUMAPlacement` now describes itself. `String()` names the Pod fields
(`NUMA placement cpuNUMA=automatic, memNUMA=3`); legacy Pods keep
`NUMA node N`. `CPUTarget()`/`MemoryTarget()` give `requested NUMA node N` or
`automatic <resource> NUMA placement`. Every message above now uses these
descriptions. Missing-allocation errors name the container, and the resize
refusal says to delete and recreate the Pod. Messages that run only after a
target is resolved are unchanged.

`TestIndependentNUMAResizeRefusalNamesFields` failed with the old message and
passes with the new one. `TestNUMAPlacementDescriptions` covers the legacy,
CPU-only and memory-only descriptions. The race suites for cpumanager,
memorymanager, topologymanager and kubelet, the explicit-file Linux suite
(78.0 s), and the Linux cross-compile and vet pass.

## Red/green evidence

Each new test failed with the source fix stashed and passed with it restored:

- `TestIndependentNUMADistanceRanking`: unrelated malformed column ignored; relevant column, local anchor and truncated row still refused.
- `TestIndependentNUMAInfoClosestOption`: missing distances load without the option and refuse with it.
- `TestIndependentNUMACommonPlacementSkipsCompletedInit`: completed init skipped; a running init on the shared pool still prevents alignment.
- `TestIndependentNUMACPUTargetRequiresEligibleCPUs`: allocatable target accepted; reserved-only and CPU-less targets refused.
- `TestPodNUMAPlacementErrorOrder`: 20 repetitions of range and Windows errors in field order.

`independent_numa_linux_test.go` gains "memory-only unrelated malformed column
ignored". Its two `preferClosest` cases now supply distances, because on Linux
`NewManager` again refuses that option without them.

## Executed checks

From the Kubernetes repository root:

- `go test -race ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/topologymanager -count=1`: pass.
- `go test -race ./pkg/kubelet -count=1`: pass.
- `go test ./pkg/apis/core/validation ./pkg/apis/core/v1 ./pkg/kubelet/apis/config/validation -count=1`: pass.
- Explicit-file Linux allocation suite (as in [verification-07.md](verification-07.md)): pass, 79.5 s.
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm` and Linux `go vet ./pkg/kubelet/cm`: pass.
- `gofmt -l` and `go vet` on touched packages: clean.

Two failures were seen, and both reproduce at the baseline with these changes
stashed. Both were already recorded as failing before this work in
[verification-03.md](verification-03.md) and [verification-04.md](verification-04.md):

- `pkg/kubelet/allocation`: `TestRetryPendingResizes` (nine resize subcases).
- `pkg/kubelet/cm/devicemanager`: `TestDevicesToAllocateConflictWithUpdateAllocatedDevices` (watch path absent).

No repository-wide `make test` run, and no Linux VM or hardware run, is claimed.

## Effect on ticket 11 evidence

The partial VM results in [verification-11.md](verification-11.md) were taken at
the baseline commit, before these corrections. Whether any reported case hit a
changed path (malformed distances, a reserved-only CPU target, the alignment
metric or the closest option) was not checked against the raw evidence. Re-run
the affected VM cases once these corrections are committed.
