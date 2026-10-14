# Ticket 02 verification

Date: 2026-10-14. Baseline: `444c8a18874898048507016288fe436ac767917a`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.

## Implemented scope

`independent-numa` is registered in policy selection, internal and external
kubelet config constants, config validation, CLI help, README policy documentation,
and generated OpenAPI descriptions. It requires Linux and pod scope. Global
policy/scope defaults remain `none`/`container`. Participating legacy Pods still
require static CPU and memory managers; ordinary Pods keep their existing
eligibility and manager behavior.

Ordinary Pods delegate shared hint merging to `single-numa-node`. Legacy
`numaNode` Pods retain the combined hard target under either policy, consuming
the prepared CPU/memory views and transaction/recovery paths. Existing node-count
limits and policy options remain in effect; `align-by-socket` is rejected.
No new Pod API fields or independent placement planner were added.

The new policy does not globally guarantee common alignment. Its success counter
compares actual CPU NUMA membership (including the shared CPU pool) with allocated
memory-block nodes across every container. A common singleton is required;
unconstrained memory, multi-node shared CPUs, missing evidence, and split outcomes
cannot increment that counter. Existing policies retain their metric behavior.

## Behavioral checks

Tests use the ticket's approved configuration, admission, manager allocation,
checkpoint recovery, state-reader, and runtime-mask seams. Policy selection,
config validation, legacy admission, CPU option compatibility, and the initial
alignment metric checks were observed failing before their implementations.

The Spec review identified that matching hints could falsely count ordinary
fractional-CPU Pods or Pods with Memory Manager `None` as aligned. Both cases
were reproduced through real allocation with a counter increment of one where
zero was required, then passed after switching to concrete resource locality.
The final real-manager matrix covers ordinary Guaranteed and BestEffort Pods,
fractional CPU, disabled Memory Manager, legacy placement, CPU and memory
shortages with spare resources elsewhere, aggregate-only ordinary CPU capacity,
failure after CPU/memory allocation, repeated refusal without leaks, checkpoint
reuse, conflicting recovery, and exact CPU/memory runtime masks.

Existing legacy hint, device-conflict, hugepage, rollback, and recovery tests now
also run with the new policy. Common Merge uses the existing specification
fixtures, including no hints and conflicting/non-preferred hints, with the
closest-node option both disabled and enabled.

## Commands and results

Commands run from the Kubernetes repository root. The existing writable Go cache
was reused with `GOCACHE=/private/tmp/numa-ticket-01-go-cache`.

| Command | Result |
| --- | --- |
| `go test ./pkg/kubelet ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/apis/config/validation ./pkg/kubelet/cm/topologymanager -run 'TestNUMAPlacementAdmission\|TestValidateStaticPolicyOptions\|TestIndependentNUMA\|TestValidateKubeletConfiguration' -count=1` | PASS during implementation. |
| `go test -race ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/apis/config/validation ./pkg/kubelet/apis/config/v1beta1 ./cmd/kubelet/app/options -count=1` | PASS, complete tests in all six packages. |
| `go test -race ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager -count=1` | PASS after the review correction. |
| Explicit Linux-source lifecycle test command below | PASS with race detection after the review correction. |
| `hack/verify-typecheck.sh ./pkg/kubelet/cm/topologymanager/... ./pkg/kubelet/apis/config/...` | PASS on all 11 configured targets during implementation. |
| `hack/verify-typecheck.sh ./pkg/kubelet/... ./cmd/kubelet/app/options/...` | PASS on all 11 configured Linux, Windows, and macOS architecture targets. |
| `hack/verify-typecheck.sh ./pkg/kubelet/cm/...` | PASS on all 11 targets after the review correction. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm -o /private/tmp/numa-ticket-02-cm-linux.test` | PASS after the review correction; compilation only. |
| `go vet ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/apis/config/validation ./cmd/kubelet/app/options` | PASS; changed managers rechecked after review. |
| `hack/verify-gofmt.sh` | PASS. |
| Changed-file `hack/verify-boilerplate.sh` and `git diff --check` | PASS. |
| `make update` | PASS after allowing pinned dependency downloads with the writable module cache. Initial sandboxed attempt failed on network access. |
| Full `make test` command below | FAIL: 162,213 tests, 417 skipped, 144 failures in 11 package groups. All subsequent vendor/tool suites passed. |

The portable allocation and Linux CRI-mask construction tests were executed on
macOS by explicitly selecting source files:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache go test -race \
  pkg/kubelet/cm/internal_container_lifecycle.go \
  pkg/kubelet/cm/internal_container_lifecycle_linux.go \
  pkg/kubelet/cm/internal_container_lifecycle_numa_test.go \
  pkg/kubelet/cm/independent_numa_linux_test.go -count=1
```

Explicit file selection bypasses file build constraints. On macOS, the fixture
uses the policy/scope constructor; normal Linux package tests additionally use
`NewManager`. This executes real CPU/Memory Managers, checkpoint serialization
and restoration, and the Linux `PreCreateContainer` implementation. It does not
execute Linux-only config/startup branches or prove Linux runtime enforcement.
Those branches are cross-compiled; no new VM/hardware evidence is claimed.

The repository-wide suite uses the repository runner, local test sockets, and
pinned test dependencies:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache \
KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-02-junit \
KUBE_TEST_ARGS='-p 8 -count=1' make test KUBE_RACE=""
```

The full run began before the review's metrics correction. All affected manager
packages and the real allocation/mask matrix were rerun on the corrected source
with race detection, alongside the platform typecheck and Linux cross-build.
Full-run evidence: `/private/tmp/numa-ticket-02-full-test.log`; JUnit output:
`/private/tmp/numa-ticket-02-junit/`.

## Full-suite result and baseline comparison

The main workspace run reported **162,213 tests, 417 skipped, and 144 failures
in 11 package groups** (1,136.891 seconds). All changed manager, kubelet,
configuration, and CLI-option packages passed. Subsequent suites passed:
vendored fileutil, 16 tests; tooling lint helpers, 51 tests with 7 skipped;
instrumentation, 38 tests.

Every failed test name and package group in this run also appears in the
recorded ticket-01 full run, which reported 151 failures in 12 package groups.
The earlier scheduler metrics failure did not recur. This is a comparison
against the earlier recorded run, not a fresh checkout-wide baseline run or a
claim that these unrelated failures are repaired. The existing baseline audits,
platform limitations, Finder artifact, and timing-sensitive failures are in
[verification-01.md](verification-01.md#baseline-failure-audit).

Failure groups:

- `pkg/kubelet/allocation`
- `pkg/kubelet/cm/devicemanager`
- `pkg/kubelet/cm/dra/plugin`
- `pkg/kubelet/kuberuntime`
- `pkg/kubelet/pluginmanager/pluginwatcher`
- `pkg/kubelet/stats`
- `pkg/volume/hostpath`
- `pkg/volume/util/hostutil`
- `test/e2e/common/node/framework/cgroups`
- `k8s.io/pod-security-admission/test`
- `k8s.io/kubectl/pkg/cmd/diff`

Comparison evidence: `/private/tmp/numa-ticket-02-failure-comparison.txt`.
The repository runner completed with exit code 2. The full-suite failure remains
a limitation, not a pass.

## Generated artifacts and workspace preservation

`make update` completed all repository update scripts. Its intended retained
change is the kubelet policy description in `pkg/generated/openapi/zz_generated.openapi.go`.
The OpenAPI snapshot step also removed `resource.k8s.io/v1beta1` from discovery
and served schema snapshots because of the baseline's alpha API-removal handling.
Those unrelated generated snapshot changes were saved for inspection in
`/private/tmp/numa-ticket-02-unrelated-generated-api.patch` and restored from the
baseline. No user-modified generated files existed at the start. No generated
files were hand-edited, and module/workspace definitions were not changed.

Pre-existing untracked planning documents outside ticket 02 remain untouched.

## Standards

Final review found no documented-standard violations or actionable baseline
smells. The explicit-file harness is documented in the test fixture.

## Spec

Final review found no remaining ticket-02 requirements missing, incorrectly
implemented, or out of scope. The alignment-metric finding was fixed and its
regressions pass. Linux runtime execution remains an evidence limitation.

Review totals: Standards 0; Spec 0 remaining findings.
