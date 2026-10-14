# Ticket 03 verification

Date: 2026-10-14. Baseline: `016029f382a2a53b9382ab4e9bd760366a25e2e8`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.

## Implemented scope

One application container can require both `cpuNUMA` and `memNUMA` under
`independent-numa` with pod scope and static CPU/Memory Managers. Distinct and
equal targets are supported; the memory target may have no CPUs. Both fields
are optional pointers, immutable, range-checked from 0 through 63, and mutually
exclusive with legacy `numaNode`. API validation retains Guaranteed resource
requirements and rejects Pod budgets, resize, and ephemeral additions.

Kubelet admission also checks static Pods, actual sparse topology, CPU-bearing
CPU targets, policy support, and the currently supported workload slice.
Partial requests, multiple applications, init/sidecars, and hugepages receive
explicit unsupported-slice refusals pending their follow-up tickets.

CPU and device hints merge together. Ordinary memory feasibility is checked
against its own singleton and excluded from the CPU/device intersection. Nil
device hint lists retain their existing no-preference meaning; empty lists
remain infeasible. Resource preferences can be overridden for explicit CPU and
memory targets, while device preference/rejection and allocation are preserved.
Explicit pairs require no distance ranking.

The existing concrete CPU/memory allocation, resource-specific affinity,
transaction snapshots, checkpoint checks, and pre-create validation now consume
the pair. No two-node union, new checkpoint format, migration, fallback target,
or checkpoint repair is introduced. Each allocator's concrete result is checked
before admission succeeds, and Linux runtime configuration uses distinct CPU
and memory masks. New/restored assignments and unrelated Pods remain protected
by the existing snapshots.

Successful split admissions increment
`kubelet_topology_manager_split_numa_admissions_total` and log both target IDs.
Common-node alignment still requires actual common singleton CPU/memory locality.
Ordinary and legacy Pod behavior remains covered by regression fixtures.

## Behavioral verification

The user confirmed these seams before new tests: Pod API validation and
serialization; kubelet admission; real CPU/Memory Manager allocation and
checkpoint recovery; Linux runtime-mask generation. Device-plugin responses
are stubbed at the plugin boundary, with all resource managers real. A final
failing provider injects a transaction failure after real allocations.

Red/green observations include lost JSON/protobuf/conversion fields, missing
API validation, ignored placement at allocation, leaked resources after a
failed attempt, unsupported workloads incorrectly admitted by kubelet, missing
split counting, topology-unaware device rejection, and kubelet resize reaching
reallocation. Tests also caught CPU requests rounded to a whole millicore and
a subsequent representation-sensitive fix. Final validation uses `AsScale(0)`
exactness and positive sign: `1000m`, `1.0`, and decimal-backed integer quantities
are accepted; `0.9999` is refused.

Real-manager fixtures cover distinct/equal targets, sparse IDs, a CPU-less
memory node, shortages with spare capacity elsewhere, repeated failures,
CPU/device conflicts, device preferences, plugin failure after CPU/memory,
failure after all three managers, and preservation of another Pod's assignments.
The allocation/mask fixture recreates managers from checkpoints repeatedly,
checks that reservations are not duplicated, injects missing CPU or memory
checkpoints, refuses conflicting recovery, and preserves restored reservations
after a later allocation failure.

## Commands and evidence

All commands run from the repository root. Unless shown otherwise, use
`GOCACHE=/private/tmp/numa-ticket-01-go-cache`; generators and the repository
runner also use `GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache`.

- Focused `Test.*NUMA` API, validation, and kubelet admission checks pass,
  including OS-field coverage and exact CPU quantity regressions.
- Complete race tests in core validation/conversion, CPU/Memory/Topology Managers,
  kubelet metrics, and kubelet pass. Local-socket tests required execution outside
  the sandbox; sandbox socket failures were rerun, not counted as passes.
- Race tests for the new real-device and resize seams pass:
  `go test -race ./pkg/kubelet/cm/devicemanager ./pkg/kubelet/allocation -run 'TestIndependentNUMADeviceAllocation|TestNUMAPlacementDeviceRollback|TestNUMAPlacementRefusesKubeletResize' -count=1`.
- `hack/verify-typecheck.sh ./pkg/apis/core/... ./pkg/kubelet/...` passes all
  11 configured platform targets. A typecheck launched while an early generator
  was replacing files was invalidated and repeated after generation completed.
- `go vet` passes the affected API, kubelet, manager, allocation, and metrics packages.
- Repository gofmt, changed-file boilerplate, and `git diff --check` pass.
- Metrics documentation-list verification and the metrics naming linter pass.
- `make update` completes all repository generators. Targeted protobuf,
  Swagger, and OpenAPI generators also pass after the final OS documentation edit.

The portable allocation and actual Linux pre-create implementation are exercised
on macOS by selecting source files explicitly:

```sh
go test -race \
  pkg/kubelet/cm/internal_container_lifecycle.go \
  pkg/kubelet/cm/internal_container_lifecycle_linux.go \
  pkg/kubelet/cm/internal_container_lifecycle_numa_test.go \
  pkg/kubelet/cm/independent_numa_linux_test.go -count=1
```

This bypasses file build constraints. On Linux, the fixture additionally uses
`NewManager`; on macOS it constructs the policy and scope directly. These tests
execute allocation, checkpoint serialization/restoration, and CRI mask
construction, not a Linux container runtime. Linux compilation uses:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm \
  -o /private/tmp/numa-ticket-03-cm-linux.test
```

The full repository suite runs once with:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache \
KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-03-junit \
KUBE_TEST_ARGS='-p 8 -count=1' make test KUBE_RACE=""
```

Logs: `/private/tmp/numa-ticket-03-full-test.log`, focused package logs
`numa-ticket-03-focused-complete.log` and `numa-ticket-03-reviewed-race.log`,
explicit-file masks `numa-ticket-03-runtime-final.log`, and quantity red/green
logs `numa-ticket-03-cpu-formats-{red,green}.log`, all under `/private/tmp/`.

The full run started before the final CPU-format review correction. Affected
packages, quantity regressions, platform typechecking, and Linux compilation
are checked again against the corrected source. No second repository-wide run
is claimed.

## Full-suite result and baseline audit

The repository runner completed with exit code 2. The main suite reports
**162,262 tests, 417 skipped, and 145 failures in 11 package groups**
(1,582.356 seconds). The subsequent vendor, lint-tool, and instrumentation
suites pass: 16 tests; 51 tests with 7 skipped; and 38 tests, respectively.

All current failure names and groups occur in the recorded ticket-01 or
ticket-02 runs. Against ticket 02 alone, two additional Device Manager names
recur: `TestNewEndpoint` and
`TestDevicesToAllocateConflictWithUpdateAllocatedDevices`; both failed in
ticket 01 with the same temporary-directory fsnotify watch race. Conversely,
`TestDevicePluginReRegistrationProbeMode` no longer fails in this run. This
explains the net change from 144 to 145 failures. It is a comparison against
prior recorded runs, not a fresh checkout-wide baseline or a claim that these
unrelated failures have been repaired.

Failure groups remain allocation, Device Manager, DRA plugin, kubelet runtime,
plugin watcher, stats, hostpath, hostutil, cgroup expectations, Pod security
fixtures, and kubectl diff. The earlier platform/artifact audits are recorded in
[verification-01.md](verification-01.md#baseline-failure-audit).
Comparison evidence: `/private/tmp/numa-ticket-03-failure-comparison.json`;
reproducer/comparison script: `/private/tmp/numa-ticket-03-summary.py`.
JUnit files: `/private/tmp/numa-ticket-03-junit/`.

The final quantity correction passes complete race tests in API validation,
Topology Manager, and kubelet (`numa-ticket-03-reviewed-race.log`). All 11
configured targets pass the affected-package typecheck after that correction
(`numa-ticket-03-typecheck-quantity-final.log`), and the final Linux build
passes (`numa-ticket-03-linux-final.log`). The full-suite failure remains a
limitation, not a pass.

## Generated artifacts

Protobuf numbers 46 and 47 follow the existing `numaNode` field 45. Regenerated
artifacts include protobuf/schema, conversion, deepcopy, OpenAPI, apply
configuration, API compatibility fixtures, and the metrics registry list.
Generated files were never hand-edited; module/workspace definitions are unchanged.

Default served-schema generation removed the baseline resource v1beta1 API.
Regenerating with the API server's
`KUBE_APISERVER_SERVE_REMOVED_APIS_FOR_ONE_RELEASE=true` compatibility setting
retains that API and limits snapshots to the new fields/OS documentation.
The alpha strict-removal setting alone did not retain it in this checkout.
The generated metrics reference page also rewrote unrelated existing metrics;
that page was restored from HEAD, with its generated diff saved in
`/private/tmp/numa-ticket-03-unrelated-metrics-documentation.patch`. The new
metric remains documented in README and the generated metric registry list.

Pre-existing untracked planning documents and other tickets remain outside this
implementation's commit.

## Standards

Final review: zero remaining findings. The inherited validator names were
expanded to `NUMAPlacement`; the injected failure provider was accepted as
transaction fault injection through the approved seam, retaining real managers.

## Spec

Final review: zero remaining findings. The reviewer caught rejection of valid
whole-CPU formats by the initial precision fix; failing regressions established
the problem, and the exact-scale/sign correction was reviewed and verified.
Later-ticket workload forms remain deliberately unsupported.

Review totals: Standards 0; Spec 0 remaining findings. No VM or hardware runtime
result is claimed; operator validation remains ticket 11.
