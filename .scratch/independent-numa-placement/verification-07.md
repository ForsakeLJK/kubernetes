# Ticket 07 verification

Date: 2026-10-14. Baseline: `5ad4df8ddb35d16bff7b592e2f4bc419c7e1fb57`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.
Implementation commit: `b9fba7e9386`.

## Implemented scope

Independent explicit, CPU-only, and memory-only requests now support ordinary
memory and all requested hugepage sizes as one singleton memory group across
applications, sequential init containers, and restartable sidecars. Memory-only
NUMA nodes and sparse IDs remain supported. Missing or insufficient capacity for
any resource refuses an explicit target; automatic placement considers only
nodes feasible for the whole group. Different page sizes cannot choose different
nodes. The Linux runtime memory mask remains exactly the selected memory node.

The temporary hugepage refusal is removed. The audit found no remaining
unsupported-slice guard from tickets 03–06. Final contract exclusions remain:
non-Linux/incompatible policies, mixed legacy/new fields, invalid IDs or resource
declarations, Pod budgets, resize, and ephemeral containers. Static-Pod checks
retain equal hugepage requests and limits. No API, generated artifact, module,
checkpoint format, runtime configuration, or deployment changed.

Automatic memory planning now treats each hugepage shortage as a rejected
memory candidate rather than a fatal error for the entire search. Exhausted
independent memory groups produce explicitly empty resource hints rather than
looking unconstrained.

Recovery validates every resource's node, presence, uniqueness, size, and
unfinished-init coverage. A surviving regular-init block may be smaller than its
request only when subsequent nonoverlapping allocations can cover the remainder.
Admission provisionally accounts for missing future assignments; start validation
requires actual reservations. Per-init reusable budgets protect the residual
needed for an unfinished init when applications are missing after recovery.
Only usable reservations transfer to new containers, preserving future blocks
and each resource's effective peak. Completed init demand remains excluded.
Independent admission validates the complete group after all containers are
allocated and rolls back the whole transaction on final validation failure.

## Behavioral verification

Tests use the previously approved public seams recorded in
[verification-03.md](verification-03.md): kubelet admission, real CPU/Memory
Manager allocation and checkpoint recovery, Linux pre-create masks, and real
Device Manager allocation with stubs only at the external plugin boundary.

Recorded red/green observations:

- The old hugepage guard refused a valid request.
- Automatic selection stopped at a node missing one page size even though
  another node could satisfy the entire group.
- A group with no joint feasible node returned no resource hints and reached
  allocation rather than failing singleton selection.
- Independent Spec review reproduced an unfinished init reaching admission and
  start with only two 2Mi pages reserved against a three-page peak. A consistent
  but undersized init checkpoint and missing application assignments both
  exposed the gap. Regressions cover ordinary memory and both hugepage sizes
  for all three forms, repeated partial failures, and repeated recovery.
- A regular application with `restartPolicy: Always` was incorrectly counted
  as a future init as well as an application. Future-init construction now
  checks actual init-container membership; the corresponding recovery
  regression covers every placement form and memory resource.

Coverage includes both 2Mi and 1Gi pages, absent sizes, insufficient individual
sizes, aggregate-only capacity, nearest feasible whole-group selection, exact
memory masks, app/init/sidecar lifetime peaks, resources requested only by inits
or sidecars, successful cleanup, and ordinary/legacy/equal-target regressions.
Matching checkpoints preserve automatic targets despite changed locality;
wrong-node, multi-node, missing, duplicate, and wrong-sized resources are refused.
Partial CPU or memory manager restoration, missing unfinished init allocations,
released completed inits, and missing applications are exercised. Repeated
failures after CPU or memory in every container preserve unrelated allocations,
all memory resources, CPU pools, and both affinities. Device tests additionally
cover CPU-compatible and incompatible topology, topology-unaware devices,
nonpreferred hints, later plugin failure, and failure after all three managers.

## Commands and results

Run from the Kubernetes repository root. Unless shown otherwise,
`GOCACHE=/private/tmp/numa-ticket-01-go-cache`; repository runners also use
`GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache`.

- Complete CPU, Memory, and Topology Manager suites, including race checks:
  `go test -race ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/topologymanager -count=1`.
- Device Manager feature/rollback race matrix:
  `go test -race ./pkg/kubelet/cm/devicemanager -run 'TestIndependentNUMA|TestNUMAPlacementDeviceRollback' -count=1`.
- Complete kubelet race suite with local socket access:
  `go test -race ./pkg/kubelet -count=1`.
- API/configuration/allocation regressions:
  `go test ./pkg/apis/core/validation ./pkg/apis/core/v1 ./staging/src/k8s.io/api/core/v1 ./staging/src/k8s.io/client-go/kubernetes/fake ./pkg/kubelet/allocation ./pkg/kubelet/apis/config/validation -run 'Test.*NUMA|Test.*Numa' -count=1`.
- `hack/verify-typecheck.sh ./pkg/kubelet/...` checks all 11 configured platform
  targets, during implementation and after the recovery correction.
- `go vet ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/devicemanager ./pkg/kubelet`.
- Repository `hack/verify-gofmt.sh`, changed-file `hack/verify-boilerplate.sh`,
  and `git diff --check`.
- `hack/verify-codegen.sh 'deepcopy|conversion'` verifies generated artifacts in
  its temporary clean worktree. No generator input/schema changed; no generated
  files were hand-edited.

Linux allocation/lifecycle source executes on macOS with explicit files:

```sh
go test -race \
  pkg/kubelet/cm/internal_container_lifecycle.go \
  pkg/kubelet/cm/internal_container_lifecycle_linux.go \
  pkg/kubelet/cm/internal_container_lifecycle_numa_test.go \
  pkg/kubelet/cm/independent_numa_linux_test.go \
  pkg/kubelet/cm/pod_wide_numa_linux_test.go \
  pkg/kubelet/cm/hugepage_numa_linux_test.go -count=1
```

This executes actual manager allocation, checkpoint I/O, recovery, transactions,
cleanup, and CRI mask construction against synthetic topology. It does not run a
Linux container or establish kernel enforcement, physical page residency, or
application performance. Hardware/runtime evidence remains a later deliverable.

Linux cross-compilation:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm \
  -o /private/tmp/numa-ticket-07-cm-linux.test
```

The full repository suite runs once with local socket access:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache \
KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-07-junit \
KUBE_TEST_ARGS='-p 8 -count=1' make test KUBE_RACE=''
```

The full suite started before the review correction. Affected manager, device,
kubelet, Linux-source, platform-typecheck, and verification checks are rerun on
the correction; no second repository-wide run is claimed.

The main suite reports **162,515 tests, 417 skipped, and 143 failures in 11
package groups** (1,151.166 seconds). The repository-wide check exits 2 and remains failed.
Vendor, lint-tool, and instrumentation suites pass: 16 tests; 51 tests with
7 skipped; and 38 tests, respectively.

Every failure name/package occurs in tickets 01–06. Compared with ticket 06,
`devicemanager/TestNewEndpoint` recurs (also recorded in ticket 01), while
`dra/plugin/TestRegistrationHandler/no-services` does not recur. The device
endpoint failure logs a temporary directory disappearing during a broad watch,
consistent with its earlier recorded filesystem-watch failure. Its isolated
race rerun passes: `go test -race ./pkg/kubelet/cm/devicemanager -run
'^TestNewEndpoint$' -count=1`. Other groups are allocation, DRA plugin, kubelet runtime,
plugin watcher, stats, hostpath, hostutil, cgroup expectations, Pod security
fixtures, and kubectl diff. This compares recorded failure identities; it is
not a fresh baseline execution or a claim those failures are repaired.

All focused and verification checks above pass on the reviewed correction.
The final application restart-policy correction was additionally checked with
all 11 Memory Manager platform typechecks, Memory/Topology Manager race suites,
the complete Linux-source race matrix, vet, formatting, boilerplate, and Linux
cross-compilation. No generator inputs changed in that final correction.

Logs: `/private/tmp/numa-ticket-07-*.log`. JUnit reports:
`/private/tmp/numa-ticket-07-junit/`. Failure comparison script:
`/private/tmp/numa-ticket-07-summary.py`.

## Standards

Final independent review of `b9fba7e9386`: zero documented-standard violations
and zero actionable baseline smells. An optional repeated rollback assertion
was consolidated into a test snapshot type and assertion helper.

## Spec

Independent review found the unfinished-init recovery coverage gap described
above. Both original repros pass after correction. The reviewer also ran 80
deterministic heterogeneous page-size, sequential-init, sidecar, and partial
checkpoint cases successfully. A subsequent application restart-policy finding
is corrected and covered. All three independent repros pass against
`b9fba7e9386`; final Spec review reports zero remaining missing requirements,
incorrect behavior, or scope creep. Evidence:
`/private/tmp/numa-ticket-07-spec-review-final.log` and
`/private/tmp/numa-ticket-07-spec-random.log`.

Review totals: Standards 0; Spec 0 remaining. No VM/hardware runtime or
performance result is claimed.
