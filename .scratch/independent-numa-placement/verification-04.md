# Ticket 04 verification

Date: 2026-10-14. Baseline: `d436e938fdeaaec49d4797ed32fac3e2b4222f6a`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.
Implementation commit: `b7115f7475d`.

## Implemented scope

One application container can declare `memNUMA` alone under `independent-numa`
with pod scope and static CPU/Memory Managers. The memory target remains hard,
including on CPU-less nodes. CPU candidates must satisfy singleton CPU capacity
and the existing device topology/preference rules. Selection retains surviving
concrete CPU assignments, then chooses a feasible common node, then the closest
directed CPU-to-memory pair. Equal known distances select the lowest CPU node ID.

The independent policy loads cAdvisor distances without requiring
`prefer-closest-numa-nodes`. Linux's `node_read_distance` emits columns in
ascending online-node ID order, and cAdvisor preserves that order. Ranking maps
the destination ID to its compact column after validating row dimensions, node
IDs, and distance values. Discovery order and sparse IDs do not become raw array
indices. Asymmetric distances retain the CPU-to-memory direction.
Source: [Linux node distance producer](https://github.com/torvalds/linux/blob/master/drivers/base/node.c)
and vendored `lib/utils/sysinfo/sysinfo.go` in cAdvisor.

Unknown or unusable relevant rows refuse ranking of multiple nonlocal
candidates, with a diagnostic suggesting both explicit targets or corrected
topology data. Restored, common, and sole feasible candidates avoid unnecessary
distance comparisons. No CPU candidates produces a capacity/topology failure.
The shared pair-ranking routine can also rank varying memory targets in ticket 05.

The resolved CPU affinity is mandatory during allocation and validated against
concrete assignments before runtime configuration. Checkpoints reconstruct the
automatic singleton; changed locality cannot move an assigned CPU target.
Multi-node or conflicting assignments refuse recovery without checkpoint repair.
Transaction snapshots release newly acquired CPU, memory, device, and affinity
state after failures. Logs record the resolved pair and whether each target was
explicit. Split and common-node metrics reflect the actual selected pair.

CPU-only, multiple-application/init/sidecar, and hugepage requests retain their
later-ticket refusals. Ordinary, legacy, and fully explicit pairs retain their
existing paths. No generated artifacts or module definitions changed.

## Behavioral verification

Tests use the seams approved in [verification-03.md](verification-03.md):
kubelet admission, real CPU/Memory Manager allocation and checkpoint recovery,
Linux runtime-mask generation, and device plugins at their external boundary.
Final-provider failures exercise the admission transaction with real allocators.

Recorded red/green cases establish the original partial-request refusal,
missing nonlocal selection, and a memory shortage initially misdiagnosed as a
CPU-candidate failure. The final fixture covers:

- Feasible common nodes, CPU-less memory, asymmetric nearest selection, equal
  distance ties, shuffled discovery order, and sparse IDs.
- Nearest-node exhaustion, sole candidates without distances, and aggregate
  CPU capacity that cannot fit on any singleton.
- Missing rows (including a single unknown candidate), short or padded rows,
  zero/unreachable values, and invalid local distances.
- Restored targets after better locality becomes available and then after
  distance data disappears; partial CPU and partial memory restoration.
- Concrete CPU sets and separate CRI CPU/memory masks, repeated restart without
  duplicate reservations, and conflicting multi-node CPU checkpoints.
- Ordinary-memory shortage, repeated post-allocation failure, preservation of
  restored/unrelated allocations, normal cleanup, and removal of both affinities.
- CPU-compatible and topology-unaware devices, device conflicts/preferences,
  plugin allocation failure, and failure after all three resource managers,
  for both explicit and automatic CPU targets.

## Commands and results

Commands run from the repository root. Unless shown otherwise,
`GOCACHE=/private/tmp/numa-ticket-01-go-cache`; repository runners also use
`GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache`.

- Complete race tests pass in CPU, Memory, and Topology Managers. Kubelet's
  complete race suite passes with local socket access after sandbox listener
  restrictions invalidated the initial kubelet run.
- Device allocation/rollback race tests pass:
  `go test -race ./pkg/kubelet/cm/devicemanager -run 'TestIndependentNUMADeviceAllocation|TestNUMAPlacementDeviceRollback' -count=1`.
- `hack/verify-typecheck.sh ./pkg/kubelet/...` passes all 11 configured platform
  targets, both during implementation and after its final changes.
- `go vet ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet` passes.
- Repository gofmt, changed-file boilerplate, and `git diff --check` pass.

The actual Linux pre-create implementation is exercised on macOS with explicit
file selection:

```sh
go test -race \
  pkg/kubelet/cm/internal_container_lifecycle.go \
  pkg/kubelet/cm/internal_container_lifecycle_linux.go \
  pkg/kubelet/cm/internal_container_lifecycle_numa_test.go \
  pkg/kubelet/cm/independent_numa_linux_test.go -count=1
```

This executes allocation, checkpoint I/O, cleanup, and CRI mask generation.
On Linux the fixture additionally constructs the policy through `NewManager`;
macOS constructs policy/scope directly because the new policy is Linux-only.
It is not evidence of a running Linux container or physical page residency.

Linux cross-compilation passes:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm \
  -o /private/tmp/numa-ticket-04-cm-linux.test
```

The full repository suite command is:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache \
KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-04-junit-unrestricted \
KUBE_TEST_ARGS='-p 8 -count=1' make test KUBE_RACE=''
```

The sandboxed full-suite attempt was stopped after discovering local socket
restrictions; the replacement ran with local socket access and exited with
code 2. Its main suite
reports **162,273 tests, 417 skipped, and 145 failures in 11 package groups**
(1,141.576 seconds). Vendor, lint-tool, and instrumentation suites pass:
16 tests; 51 tests with 7 skipped; and 38 tests, respectively.

Every failure name and package occurs in a recorded ticket-01, -02, or -03 run.
Compared with ticket 03, Device Manager's `TestRun` recurs from ticket 01 while
`TestDevicesToAllocateConflictWithUpdateAllocatedDevices` no longer fails,
leaving the total at 145. This is a comparison of recorded failures, not a new
checkout-wide baseline or a claim that those failures are repaired. Failure groups remain allocation,
Device Manager, DRA plugin, kubelet runtime, plugin watcher, stats, hostpath,
hostutil, cgroup expectations, Pod security fixtures, and kubectl diff.
The full suite remains a failed check, despite the focused feature checks passing.

Logs are in `/private/tmp/numa-ticket-04-*.log`; JUnit reports are in
`/private/tmp/numa-ticket-04-junit-unrestricted/`. Failure-name comparison against
earlier ticket runs uses `/private/tmp/numa-ticket-04-summary.py` and writes
`/private/tmp/numa-ticket-04-failure-comparison.json`.

## Standards

Review against the pinned baseline: zero documented-standard violations and
zero actionable smell findings. Changes remain at the approved behavior seams;
no ADR files exist to check for conflicts.

## Spec

Review against the pinned baseline: zero actionable Spec findings. The ticket's
selection, refusal, enforcement, rollback, cleanup, and recovery requirements
are covered, with later-ticket workload forms still refused and no scope creep.

Review totals: Standards 0; Spec 0. No VM/hardware runtime execution or performance
result is claimed; operator validation remains ticket 11.
