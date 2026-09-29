# Ticket 06 execution record — 2026-09-29

## Environment

- Fork commit under test: `5428db8ad07` plus this ticket's documentation and checker changes.
- Operator workspace: macOS Darwin arm64, Go 1.27.1.
- Linux Kubernetes node: **unavailable**.
- Fork API server and kubelet running on a multi-NUMA node: **unavailable**.
- CRI runtime and per-node CPU/memory/hugepage capacity: **unavailable**.
- Device plugin with a conflicting topology hint: **unavailable**.

## Automated results

All Go commands used `GOCACHE=/private/tmp/numa-ticket06-go-cache` because
the default macOS cache path was not writable in this workspace.

| Command | Result |
| --- | --- |
| `python3 -m py_compile runtime/check-masks.py`; parser smoke check for singleton and range lists | PASS |
| `go test -run '^$' ./pkg/apis/core/validation ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm` | PASS: affected packages typecheck on macOS |
| `go test -count=1 -run 'Test(PodNUMAPlacement\|NUMAPlacement\|RequestedNUMA\|StaticPolicyRequestedNUMA\|ExplicitNUMA)'` on the same packages | PASS: all matched tests; `pkg/kubelet/cm` has no matching macOS tests |
| `go test -count=1 ./pkg/apis/core/validation ./pkg/kubelet/cm/cpumanager ./pkg/kubelet/cm/memorymanager ./pkg/kubelet/cm/topologymanager ./pkg/kubelet/cm/devicemanager ./pkg/kubelet/cm` | Four packages and `pkg/kubelet/cm` PASS; device manager FAILS because this sandbox disallows binding Unix sockets in its unrelated endpoint tests |
| `go test -count=1 -run '^TestNUMAPlacementDeviceRollbackPreservesExistingAssignments$' ./pkg/kubelet/cm/devicemanager` | PASS |
| `GOOS=linux GOARCH=arm64 go test -c -o /private/tmp/numa-ticket06-cm-linux.test ./pkg/kubelet/cm` | PASS: Linux test binary built; not run on macOS |
| `timeout 180 go test ./...` | INCOMPLETE (exit 124): repository-wide run timed out after 180 seconds; unrelated API server, kubeadm, and kubelet tests also failed because this sandbox disallows binding TCP/Unix sockets. No full-suite pass is claimed. |

Prior ticket results are in the answers to
[02](../issues/02-enforce-single-container-placement.md),
[03](../issues/03-enforce-pod-wide-placement.md),
[04](../issues/04-confine-hugepages.md), and
[05](../issues/05-preserve-placement-on-restart.md); those results are not a
substitute for rerunning relevant regression tests at this commit.

## Host-dependent runtime evidence

All rows below are **NOT RUN** because this workspace has no Linux multi-NUMA
host or fork API server/kubelet deployment. No runtime acceptance is claimed.

| Case | Result | Missing evidence |
| --- | --- | --- |
| NUMA 0 and other node: app, init, sidecar effective CPU/memory masks | NOT RUN | Running containers and `/proc/self/status` masks |
| Ordinary memory and each supported hugepage size | NOT RUN | Per-node allocation and process masks |
| Container and kubelet restart | NOT RUN | Before/after container IDs and masks |
| CPU, ordinary-memory, hugepage shortage with spare capacity elsewhere | NOT RUN | Kubelet refusals and per-node capacity |
| Invalid/absent IDs, unsupported configuration, ineligible declarations | NOT RUN | API errors and kubelet status/events |
| Immutable update, resize, ephemeral-container addition | NOT RUN | API errors and status/events |
| Partial failure and normal cleanup | NOT RUN | Before/after manager assignments and successful retry |
| Existing device-policy interaction | NOT RUN | Device plugin topology and refusal |
| Equivalent omitted-field Pods | NOT RUN | Admission, lifecycle, and actual masks |

The guarantee under evaluation is CPU affinity and memory-allocation
confinement. This record makes no claim about physical locality of every
shared/cached page or application performance.
