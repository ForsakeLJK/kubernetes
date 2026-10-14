# Ticket 01 verification

Date: 2026-10-14. Baseline: `621a7c5c03ed3625065078567bc26773cebd75d7`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Go 1.27.1.

## Implemented scope

Legacy requirements resolve to separate internal CPU and memory targets with an
independent participation flag. Ordinary Pods keep their existing allocation
path. No public API fields, generated artifacts, policies, or module files changed.

Memory Manager reads an optional resource-specific affinity view; existing
stores retain shared-affinity fallback. The topology manager forwards that view,
preserves both affinities across container restart cleanup, and restores earlier
affinities after failed admission.

Memory rollback calculates this Pod's reservation and assignment-count changes
on each affected node from its single-node placement blocks. It applies those
deltas to current accounting, retaining unrelated allocations, restored init
blocks, hugepages, and init reuse. It rejects inconsistent multi-node blocks
rather than silently losing accounting; participating Pods already require
single-node blocks. CPU and device rollback contracts remain unchanged.

## Behavioral checks

Tests use Topology Manager admission/affinity access and CPU/Memory Manager
allocation, rollback, recovery, and state-reader interfaces, as identified in
the approved ticket and validation plan.

The added rollback test first failed with a nil-pointer panic in
`SnapshotNUMAPlacement`, then passed after removing its dependency on the legacy
API field. It exercises ordinary-memory and hugepage changes across nodes,
restored init reuse, and another Pod's intervening allocation. Other additions
check shared-store fallback, separate memory affinity through real allocation,
restart cleanup, failed-admission affinity restoration, and explicit node zero
overriding ordinary placement preferences. Existing tests cover CPU/device
shortages, injected provider/container failure, whole-Pod rollback, and valid
and conflicting checkpoint recovery.

All commands run from the Kubernetes repository root. A writable cache was
necessary because the default `~/Library/Caches/go-build` was denied by the
sandbox. Substitute another writable cache path if needed.

| Command | Result |
| --- | --- |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache go test ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager -run 'Test(RequestedNUMA\|NUMAPlacement\|MemoryAffinity)' -count=1` | PASS, all three packages. Repeated during implementation. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache go test -race ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager -count=1` | PASS, full tests in all three packages with race detection. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache go vet ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager` | PASS. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache hack/verify-typecheck.sh ./pkg/kubelet/cm/...` | PASS on all 11 configured Linux, Windows, and macOS architecture targets. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./pkg/kubelet/cm -o /private/tmp/numa-ticket-01-cm-linux.test` | PASS; compilation only. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache hack/verify-gofmt.sh` | PASS. |
| `hack/verify-boilerplate.sh` | FAIL on existing year-2026 headers outside this ticket. The new file and the modified memory test now use the repository's year-free template. |
| `git diff --cached --name-only -- '*.go' \| xargs hack/verify-boilerplate.sh` | PASS for every added or modified Go file. |
| `git diff --check` | PASS. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-01-junit make test KUBE_RACE=""` | BLOCKED before test execution: `gotestsum` installation attempted a download from `proxy.golang.org`, unavailable in the network-restricted environment. |
| Main Go workspace unit suite via direct `go test` | Stopped after sandbox-denied local socket binds; no complete-suite result. |
| `GOCACHE=/private/tmp/numa-ticket-01-go-cache GOMODCACHE=/private/tmp/numa-ticket-01-go-modcache KUBE_JUNIT_REPORT_DIR=/private/tmp/numa-ticket-01-junit make test KUBE_RACE=""` with sandbox escalation | FAIL: 151 failures in the main suite; the changed manager packages and subsequent vendor/tool suites passed. See totals below. |

## Full-suite attempts

After the repository runner failed before executing tests, the fallback selected
all 1,474 main-workspace packages with test files, using the same exclusions as
`hack/make-rules/test.sh` for integration, end-to-end, and third-party tests.
It ran `go test -json -timeout=180s -p 8 -count=1` over that list, with
`KUBE_CACHE_MUTATION_DETECTOR=true` and `KUBE_PANIC_WATCH_DECODE_ERROR=true`.
Race detection was covered separately in the changed manager packages.

The direct run also encountered sandbox-denied TCP/Unix socket binds and was
stopped. No complete-suite pass/fail result is inferred from that attempt.
After sandbox escalation, the repository's actual `make test` harness was
restarted with permission to download its pinned tools and open local test
sockets. This is the authoritative full-suite attempt, including the standalone
`hack/tools` test modules selected by the harness.

Evidence: `/private/tmp/numa-ticket-01-full-test-unrestricted.log` and JUnit
results under `/private/tmp/numa-ticket-01-junit/`. Earlier attempt logs are
`/private/tmp/numa-ticket-01-full-test.log` and
`/private/tmp/numa-ticket-01-workspace-tests.jsonl`.

Final result: `make test` completed with exit code 2. The main workspace
reported **162,170 tests, 416 skipped, 151 failures in 12 package groups**
(1,192.787 seconds). All changed CPU, memory, and topology manager packages
passed. The subsequent suites passed: vendored fileutil, 16 tests; tooling
lint helpers, 51 tests with 7 skipped; instrumentation, 38 tests. This full-suite
failure is retained as a limitation, not presented as a pass.

Main-suite failure groups:

- `pkg/kubelet/allocation`
- `pkg/kubelet/cm/devicemanager`
- `pkg/kubelet/cm/dra/plugin`
- `pkg/kubelet/kuberuntime`
- `pkg/kubelet/pluginmanager/pluginwatcher`
- `pkg/kubelet/stats`
- `pkg/scheduler/framework/runtime`
- `pkg/volume/hostpath`
- `pkg/volume/util/hostutil`
- `test/e2e/common/node/framework/cgroups`
- `k8s.io/pod-security-admission/test`
- `k8s.io/kubectl/pkg/cmd/diff`

The audit below distinguishes baseline reproductions, environment artifacts,
and timing-sensitive failures. No failure was found in the changed manager
package suites.

## Baseline failure audit

The permitted full-suite run reports failures in allocation, device manager,
DRA plugin, kubelet runtime, and stats packages. A Go source overlay restored
all 11 changed, previously tracked Go files to `621a7c5c03e` without modifying
the checkout; the unused new resolver file has no initialization or callers in
the baseline sources. The following baseline command reproduced failures in
all five package groups:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
KUBE_CACHE_MUTATION_DETECTOR=true KUBE_PANIC_WATCH_DECODE_ERROR=true \
go test -overlay /private/tmp/numa-ticket-01-baseline/overlay.json \
  -json -count=1 -timeout=180s \
  ./pkg/kubelet/allocation ./pkg/kubelet/cm/devicemanager \
  ./pkg/kubelet/cm/dra/plugin ./pkg/kubelet/kuberuntime ./pkg/kubelet/stats
```

This baseline check required the same sandbox escalation for local test
sockets. Output: `/private/tmp/numa-ticket-01-baseline-tests.jsonl`.
Baseline failing test-event counts (including parent/subtests): allocation 78,
device manager 5, DRA plugin 8, runtime 41, stats 2. DRA diagnostics include
macOS Unix-socket path-length failures. Device tests include file-watcher and
re-registration failures. Resize and stats assertions also fail on baseline;
these are not treated as successful checks.

The additional plugin-watcher failure also reproduced on baseline:

```sh
GOCACHE=/private/tmp/numa-ticket-01-go-cache \
go test -overlay /private/tmp/numa-ticket-01-baseline/overlay.json \
  -json -count=1 -timeout=180s ./pkg/kubelet/pluginmanager/pluginwatcher \
  -run '^TestPluginReRegistration$'
```

It timed out waiting for socket unregistration, matching the full-suite
failure. Output: `/private/tmp/numa-ticket-01-baseline-pluginwatcher.jsonl`.

Additional full-suite failures occurred in `pkg/volume/hostpath`,
`pkg/volume/util/hostutil`, and `test/e2e/common/node/framework/cgroups`.
The matching tests also failed with baseline sources (`TestOSFileTypeChecker`,
`TestGetFileType`, and `TestGetCPULimitCgroupExpectations`). Hostutil explicitly
reports that this platform is unsupported. Evidence:
`/private/tmp/numa-ticket-01-baseline-other-tests.jsonl`.

`pkg/scheduler/framework/runtime` reported a zero-latency assertion in
`TestRecordingMetrics/Unreserve_-_Success`. The same metrics test passed in the
baseline retry and in a current-source retry (`go test ./pkg/scheduler/framework/runtime -run '^TestRecordingMetrics$' -count=1` with the writable cache). It is recorded as a timing-sensitive full-suite failure, separately from reproduced baseline failures.

`k8s.io/pod-security-admission/test` also reports an unexpected existing
`testdata/.DS_Store` fixture file. This Finder artifact is unrelated to the
implementation and was left in place. A baseline `TestFixtures` retry
reproduced the same failure; output is
`/private/tmp/numa-ticket-01-baseline-fixtures.log`. The package is in staging
and does not consume the modified kubelet manager code.

`k8s.io/kubectl/pkg/cmd/diff` timed out after three minutes in the unchanged
`TestDiffProgram`, which executes host `diff` on `/dev/zero` twice. The orphaned
`diff /dev/zero /dev/zero` process was identified by PID and command, then
terminated. This staging test does not consume the changed manager code.
It was not rerun for another timeout; its result remains a recorded platform
limitation rather than a claimed baseline reproduction.

## Review and limits

Parallel standards and specification reviews against the implementation diff
from `621a7c5c03e`: zero substantive findings on each axis. Verification evidence
was recorded after those reviews.

No Linux runtime, NUMA masks on a running host, VM acceptance, or performance
measurement was executed. Cross-compilation is not runtime validation. Ticket
01 preserves legacy enforcement and prepares later tickets; it does not enable
independent placement.
