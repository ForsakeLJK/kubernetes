# Ticket 06 verification

Date: 2026-10-14. Baseline: `cbfff5ef632e60f7c81fc3759e3b13c6445bd582`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.

## Implemented scope

Explicit pairs, CPU-only requests, and memory-only requests now support multiple
application containers, sequential init containers, and restartable sidecars.
One CPU/memory node pair covers the Pod. Concrete CPU sets and memory quantities
retain existing effective-resource calculations and container lifetime overlap.
Every covered container's current declarations are checked, including static
Pod and pre-start paths; a cached Guaranteed QoS status cannot bypass them.
Hugepage requests and limits remain explicitly unsupported for new-field Pods
until ticket 07.

Automatic target reconstruction includes surviving init and sidecar assignments,
including zero-sized memory blocks that record already reused memory. Completed
sequential inits are excluded from future demand and their released assignments
are not recreated. Surviving completed init assignments can still establish the
pair and supply reusable reservations. Conflicting sibling CPU or memory nodes
refuse both admission and pre-start validation without changing checkpoints.

Partial restoration uses container lifetimes. A missing application cannot take
another application's CPU or device. A missing sequential init can reuse future
application, sequential init, and later sidecar allocations; preceding sidecars
overlap it and remain excluded. Memory charges only the missing init's residual
demand beyond future reservations, preserving future memory blocks and the
original effective peak. A shared overlap predicate keeps CPU, memory, and
device reconstruction consistent.

Device hints combine surviving assignments with capacity for missing containers,
keeping surviving devices mandatory in CPU topology coordination. Completed init
allocations do not impose future device demand. Existing device preference,
allocation, and checkpoint behavior remains in place for ordinary and legacy
Pods. No additional device-locality guarantee is introduced.

Transaction cleanup restores both affinities, CPU/memory/device assignments,
init reuse, and only newly acquired reservations after failures in later
providers or containers. It preserves restored and unrelated assignments and
restores reuse entries pruned during a failed attempt without overwriting
intervening unrelated work. No checkpoint format, generated artifact, module
file, runtime configuration, or deployment changed.

## Behavioral verification

Tests use the seams confirmed in [verification-03.md](verification-03.md):
kubelet admission, real CPU/Memory Manager allocation and checkpoint recovery,
Linux pre-create masks, and device plugins at their external boundary. Injected
providers fail transactions after real allocation; plugin failures occur at the
external plugin boundary.

Recorded red/green observations include the temporary multiple/init refusal,
reallocation of a released completed init, runtime start accepting conflicting
sibling nodes, partial recovery sharing a CPU between simultaneous apps, lost
device reuse after rollback, incomplete device checkpoints rejected as complete
request mismatches, completed init device requests imposing future demand, and
cached Guaranteed QoS status bypassing unequal memory declarations. Spec review independently reproduced an unfinished init recovery failure for
all three request forms and an isolated memory recovery failure. The fixes and
new regressions pass, including a later regular init holding reusable resources.

Coverage includes:

- All three forms with multiple apps, sequential init reuse, and sidecars both
  before and after a regular init; exact effective CPU/memory peaks and normal
  cleanup of reservations and both affinities.
- Per-container fractional CPU, unequal memory declarations, and hugepage
  requests/limits in apps, inits, and sidecars; valid declarations accepted.
- CPU and memory shortages with spare resources on other nodes, aggregate-only
  automatic capacity, and sidecar overlap shortages; repeated refusals leak no
  reservations.
- Failure after CPU and after memory at every init/sidecar/application boundary,
  repeated failures, clean subsequent admission, and partial recovery rollback.
- Checkpoint recreation, repeated recovery, partial CPU or memory manager state,
  missing earlier app allocation, released completed init, init-only surviving
  targets despite changed locality, and inconsistent sibling resource nodes.
- Missing unfinished init allocations with earlier/later sidecars and later
  sequential inits, repeated rollback, unchanged future assignments, and exact
  memory reservation peaks.
- CPU-compatible/topology-unaware devices, incompatible or nonpreferred hints,
  plugin failure in a later container, failure after all three managers, and
  preservation of unrelated allocations and device reuse.
- Partial device checkpoints missing an application or an unfinished init,
  completed released device init, surviving device preservation, repeated
  rollback, and successful allocation of distinct devices to simultaneous apps.
- Existing single-container, ordinary, and legacy allocation/mask regressions.

## Commands and results

Commands run from the repository root. Unless shown otherwise,
`GOCACHE=/private/tmp/numa-ticket-01-go-cache`; repository runners also use
`GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache`.

- Complete CPU, Memory, and Topology Manager race suites pass:
  `go test -race ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/topologymanager -count=1`.
- Focused Device Manager race tests pass:
  `go test -race ./pkg/kubelet/cm/devicemanager -run 'TestIndependentNUMA|TestNUMAPlacementDeviceRollback' -count=1`.
- Kubelet's complete race suite passes with local socket access:
  `go test -race ./pkg/kubelet -count=1`.
- `hack/verify-typecheck.sh ./pkg/kubelet/...` passes all 11 configured platform
  targets during implementation and after the review changes.
- `go vet ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/devicemanager ./pkg/kubelet` passes.
- Repository gofmt, changed-file boilerplate, and `git diff --check` pass.

Actual Linux pre-create implementation, real allocation, checkpoint I/O,
recovery, rollback, cleanup, and CRI masks execute on macOS with explicit files:

```sh
go test -race \
  pkg/kubelet/cm/internal_container_lifecycle.go \
  pkg/kubelet/cm/internal_container_lifecycle_linux.go \
  pkg/kubelet/cm/internal_container_lifecycle_numa_test.go \
  pkg/kubelet/cm/independent_numa_linux_test.go \
  pkg/kubelet/cm/pod_wide_numa_linux_test.go -count=1
```

This passes. It is not evidence of a running Linux container, actual kernel
masks, physical page residency, or application performance. The existing fixture
uses `NewManager` on Linux and directly constructs the scope on macOS; the new
Pod-wide fixture directly constructs the scope and real resource managers.

Linux cross-compilation passes:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm \
  -o /private/tmp/numa-ticket-06-cm-linux.test
```

The full repository suite runs once with local socket access:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache \
KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-06-junit \
KUBE_TEST_ARGS='-p 8 -count=1' make test KUBE_RACE=''
```

The full suite started before the final cached-QoS correction. Affected kubelet
and topology packages, Linux runtime-mask tests, and platform typechecks were
rerun against that correction; no second repository-wide run is claimed.
The main suite reports **162,408 tests, 417 skipped, and 143 failures in 10
package groups** (1,211.516 seconds). Vendor, lint-tool, and instrumentation
suites pass: 16 tests; 51 tests with 7 skipped; and 38 tests, respectively.
The full repository check remains failed.

Every failure name and package occurs in ticket 05 and in the earlier recorded
runs. Device Manager passes in this run; ticket 05's
`TestDevicePluginReRegistrationProbeMode` and `TestGetPreferredAllocation`
failures do not recur. The remaining groups are allocation, DRA plugin, kubelet
runtime, plugin watcher, stats, hostpath, hostutil, cgroup expectations, Pod
security fixtures, and kubectl diff. This compares recorded names/packages;
it is not a fresh baseline run or a claim that those failures are repaired.

Logs are in `/private/tmp/numa-ticket-06-*.log`; JUnit reports are in
`/private/tmp/numa-ticket-06-junit/`. The comparison script is
`/private/tmp/numa-ticket-06-summary.py`; its output is
`/private/tmp/numa-ticket-06-failure-comparison.json`.

## Standards

Final independent review: zero documented-standard violations and zero
actionable baseline smell findings. The shared lifetime predicate centralizes
init/sidecar overlap while provider-specific accounting remains in each manager.

## Spec

Final independent review: zero remaining missing requirements, scope creep, or
incorrect implementation findings. The unfinished-init recovery finding was
fixed and independently rechecked through initially failing CPU and memory
repros, plus the repository's extended regression cases. The final declaration
QoS correction and its cached-status regression were reviewed on both axes.

Review totals: Standards 0; Spec 0 remaining. No VM/hardware runtime or
performance result is claimed; operator validation remains ticket 11.
