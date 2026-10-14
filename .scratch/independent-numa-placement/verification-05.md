# Ticket 05 verification

Date: 2026-10-14. Baseline: `d299b0de3b8e5df559c4d9e3dd19915117a5fb89`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.
Implementation commit: `89624562382`.

## Implemented scope

One application container can declare `cpuNUMA` alone under `independent-numa`
with pod scope and static CPU/Memory Managers. The CPU target remains mandatory,
including device topology coordination. Automatic ordinary memory must fit on
one node; that node may have no CPUs. Selection preserves a surviving memory
assignment, then prefers a feasible common node, then uses ticket 04's shared
CPU-to-memory distance ranking and ascending-ID ties. Sparse IDs and shuffled
discovery retain the producer's compact ascending-ID columns.

Multiple nonlocal memory candidates with unusable relevant distance data cause
refusal. Explicit pairs, restored targets, feasible common nodes, and sole
candidates need no distance ranking. Aggregate memory across nodes cannot satisfy
an automatic singleton request. CPU shortages and incompatible device topology
remain CPU/device failures rather than being hidden by memory selection.

The resolved memory affinity is mandatory for allocation, capacity accounting,
reuse, pre-start validation, and runtime-mask generation. Matching concrete
checkpoint assignments reconstruct the automatic target; changed distances do
not move it. Partial recovery constrains selection by surviving assignments.
Conflicting or multi-node restored memory refuses admission and container start.
Rollback covers both managers and both affinities, preserves restored/unrelated
allocations, and never dereferences an absent memory API field.

CPU-only, memory-only, explicit-pair, legacy, and ordinary fixtures exercise their
actual allocation and CRI masks. Multiple application/init/sidecar containers
and hugepages retain the refusals assigned to later tickets. No generated files,
module definitions, checkpoint formats, or runtime configuration changed.

## Behavioral verification

Tests use the seams confirmed in [verification-03.md](verification-03.md):
kubelet admission, real CPU/Memory Manager allocation and checkpoint recovery,
Linux pre-create masks, and device plugins at their external boundary. The
initial CPU-only admission test failed on the temporary refusal before passing
with the new implementation.

The final fixtures cover:

- Common targets, CPU-less automatic memory, asymmetric directed nearest
  selection, equal known distance ties, shuffled discovery, and sparse IDs.
- Exhausted nearer memory, sole feasible nodes without distances, CPU shortages,
  and memory requests that fit only by aggregating multiple nodes.
- Missing CPU-source rows despite available reverse rows, short and padded rows,
  unknown/unreachable candidates, and invalid local distances.
- Automatic memory retained after a nearer candidate becomes available in the
  distance data and again after distance data disappears; partial CPU and memory
  restoration; repeated recovery without duplicate accounting.
- Separate concrete CPU/memory masks; conflicting multi-node memory checkpoints
  refused by admission and pre-start checks without changing their reservations.
- Repeated post-allocation failures; preservation of complete and partial restored
  assignments; preservation of unrelated assignments; normal cleanup and removal
  of both affinities.
- CPU-compatible and topology-unaware devices, CPU/device conflicts and preserved
  device preferences, plugin failures after CPU/memory allocation, and failures
  after all three managers, with common and remote automatic memory targets.
- Kubelet support for omitted memory, missing/CPU-less CPU nodes, and range checks.

## Commands and results

Commands run from the repository root. Unless shown otherwise,
`GOCACHE=/private/tmp/numa-ticket-01-go-cache`; repository runners also use
`GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache`.

- Complete race suites pass in CPU, Memory, and Topology Managers:
  `go test -race ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/topologymanager`.
- Kubelet's complete race suite passes with local socket access:
  `go test -race ./pkg/kubelet -count=1`. The sandboxed attempt failed on local
  listener restrictions and was rerun; it is not counted as a pass.
- Device allocation/rollback race tests pass:
  `go test -race ./pkg/kubelet/cm/devicemanager -run 'TestIndependentNUMADeviceAllocation|TestNUMAPlacementDeviceRollback' -count=1`.
- `hack/verify-typecheck.sh ./pkg/kubelet/...` passes all 11 configured platform
  targets during implementation and after the final changes.
- `go vet ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/devicemanager ./pkg/kubelet` passes.
- Repository gofmt, changed-file boilerplate, and `git diff --check` pass.

The actual Linux pre-create implementation is executed on macOS with explicit
file selection:

```sh
go test -race \
  pkg/kubelet/cm/internal_container_lifecycle.go \
  pkg/kubelet/cm/internal_container_lifecycle_linux.go \
  pkg/kubelet/cm/internal_container_lifecycle_numa_test.go \
  pkg/kubelet/cm/independent_numa_linux_test.go -count=1
```

This passes and executes allocation, checkpoint I/O, recovery, cleanup, and CRI
mask generation. On Linux the fixture additionally constructs the policy through
`NewManager`; macOS constructs policy/scope directly because the policy is
Linux-only. This is not evidence of a running Linux container or physical page
residency.

Linux cross-compilation passes:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm \
  -o /private/tmp/numa-ticket-05-cm-linux.test
```

The full repository suite runs once, with local socket access:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache \
KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-05-junit \
KUBE_TEST_ARGS='-p 8 -count=1' make test KUBE_RACE=''
```

The command exits with code 2. The main suite reports **162,289 tests, 417 skipped, and 145 failures in 11
package groups** (1,122.958 seconds). Vendor, lint-tool, and instrumentation
suites pass: 16 tests; 51 tests with 7 skipped; and 38 tests, respectively.

Every failure name and package occurs in an earlier ticket-01 through ticket-04
record. Compared with ticket 04, Device Manager's
`TestDevicePluginReRegistrationProbeMode` and `TestGetPreferredAllocation` recur
from earlier runs, while `TestNewEndpoint` and `TestRun` no longer fail. The total
remains 145. This compares recorded names/packages; it is not a new baseline run
or a claim that the broad failures are repaired. Failure groups remain allocation,
Device Manager, DRA plugin, kubelet runtime, plugin watcher, stats, hostpath,
hostutil, cgroup expectations, Pod security fixtures, and kubectl diff. The full
repository suite remains a failed check despite the focused feature checks
passing.

Logs are in `/private/tmp/numa-ticket-05-*.log`; JUnit reports are in
`/private/tmp/numa-ticket-05-junit/`. Failure-name comparison with earlier ticket
runs uses `/private/tmp/numa-ticket-05-summary.py` and writes
`/private/tmp/numa-ticket-05-failure-comparison.json`.

## Standards

Independent review against the pinned baseline: zero documented-standard
violations and zero actionable smell findings. Automatic memory validation
follows the existing CPU-manager convention, and selection shares one planner.

## Spec

Independent review against the pinned baseline: zero missing/partial
requirements, zero scope-creep findings, and zero incorrect implementation
findings. Selection, singleton enforcement, partial recovery, rollback, device
coordination, and compatibility match ticket 05; later-ticket workload forms
remain refused.

Review totals: Standards 0; Spec 0. No VM/hardware runtime execution or performance
result is claimed; operator validation remains ticket 11.
