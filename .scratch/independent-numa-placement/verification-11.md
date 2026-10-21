# Ticket 11: user-operated VM runtime progress

Recorded: **2026-10-19**, updated **2026-10-21**. Ticket status: **claimed / in progress**.
STREAM VM smoke now passes with locally reviewed raw evidence. This does not
close ticket 11 or declare the full runtime matrix complete.

## STREAM update: 2026-10-21

The [reviewed raw evidence](runtime/evidence/2026-10-21/README.md) reproduces
PASS_SMOKE exactly: 36/36 executions, 2,424 matching artifact hashes, correct
CPU/worker placement, numerical validation and target-node residency for all
1,954 queried base pages per array (108 array observations; 4KiB pages).
All archive retrieval and Pod deletion commands succeeded. This is pre-timing
guest residency, with unknown LLC and no qualified bandwidth or DDR/HBM claim.
The test uses the earlier b76f73a6 binary revision, not subsequent corrections.
The October 19 narrative and matrix below remain historical; their missing raw
batches and untested variants are not filled in by this STREAM run.

## Evidence and scope

The user executed the supplied commands/scripts inside their QEMU Ubuntu VM
and returned results in this conversation. The assistant did not execute tests
in the guest. Full mask JSON, refusal details, checkpoint summaries and several
batch transcripts were supplied; compatibility/API and residency results were
reported as aggregate summaries. PASS below means **operator-reported PASS for
the stated subcase**, not independent inspection of every VM artifact.

The [dated evidence directory](runtime/evidence/2026-10-19/README.md) retains
the four provided environment captures, local operator-kit snapshots, binary
build provenance/checksums and a
[structured transcription](runtime/evidence/2026-10-19/reported-results.json)
of the reported results. Complete raw VM run directories, effective `configz`,
resolved workload image IDs, and individual residency observations have not
been imported or independently reviewed here. Operator-kit snapshots identify
what was provided locally; the VM copies' script hashes were not supplied.

The numbers **15**, **102**, **7** and **22** describe different runner checks
or cases and are not additive counts of the kit's 34 acceptance categories.
The 102-check batch includes command success, setup and capture assertions.
No complete `runtime/report.py` acceptance report has been generated from this
partial collection.

## Environment and deployment

| Item | Recorded value and source |
| --- | --- |
| Fork | `numa_support_hbm`, commit `b76f73a6f478ef3ed63cdf58bcb295dfcec1013b`; user readiness capture |
| Guest checkout | `/home/jukeliu/k8s_workspace/kubernetes` |
| Guest binaries | `/home/jukeliu/k8s_workspace/ticket11-bin` |
| Build provenance | All six Mac Linux/ARM64 builds were inspected: Go 1.27.1, expected `vcs.revision`, `vcs.modified=false`; SHA-256 manifest retained. The VM launcher checks these hashes; its raw startup capture has not been imported. |
| Displayed version | `v0.0.0-master+$Format:%H$`; this placeholder alone does not establish fork identity |
| VM/host boundary | QEMU ARM64, `virt,accel=hvf`, Apple host; SSH host port 2222. Physical backing memory types/mapping are unverified. |
| Guest | Ubuntu 26.04.1 LTS, aarch64, kernel `7.0.0-34-generic` |
| Kubernetes node | `ubuntu-numa-test` |
| NUMA 0 | CPUs `0-3`, `MemTotal: 3532200 kB` |
| NUMA 1 | CPUs `4-7`, `MemTotal: 4054908 kB` |
| Directed distances | Node 0: `10 20`; node 1: `20 10`; online IDs `0-1` |
| Runtime | containerd 2.2.2, CRI plugins healthy, `SystemdCgroup=true`; CNI files/binaries present |
| Launch configuration | CPU Manager `static`; Memory Manager `Static`; Topology Manager `independent-numa`; scope `pod`; systemd cgroups |
| CPU reservations | `reservedSystemCPUs: 0,4`, leaving three exclusive CPUs on each node |
| Memory reservations | System 512Mi + Kubernetes 1024Mi + memory eviction 100Mi = 1636Mi; `reservedMemory` accounts 818Mi on each node |
| Swap | 4GiB enabled at baseline; launcher runs `swapoff -a` and sets `failSwapOn=true`. Its saved post-change artifact remains in the VM. |
| Workload image | Test manifests use `busybox:1.36`; actual runtime image IDs are expected in saved Pod JSON but have not been supplied locally |
| Probe | Existing `runtime/probe.c`, compiled statically in guest, copied into the container, verified by hash, then executed as PID 1; compiler/source/binary records remain in the VM residency archive |
| Hugepages/devices | Baseline reports no provisioned explicit hugepages; no topology-aware device-plugin evidence supplied |

Launch settings are grounded in the supplied launcher and tested behavior;
the complete effective kubelet configuration still needs archival review.
The initial `configz` request failed with `BadRequest: address not allowed`.
Node readiness and workload exec tests proceeded successfully. No subsequent
successful `configz` response was supplied.

## Completed functional observations

### Placement and container restarts

| Pod/form | Observed CPUs | CPU node | Memory mask | Result |
| --- | --- | --- | --- | --- |
| `split-test` / explicit | `1` | 0 | `1` | PASS |
| `cpu-only-test` | `2` | 0 | `0`, automatic | PASS |
| `memory-only-test` | `5` | 1, automatic | `1` | PASS |

The same-node automatic choices are feasible common-node cases, not evidence
of farther-node ranking or selection when the common node cannot fit.
All three single-application containers were stopped through containerd and
restarted by kubelet. The runner checked new container identity and unchanged
Pod UID; the supplied transcript shows restart counts `0 -> 1` and the same
masks. `split-test` also retained its masks after the initial update attempts;
the complete API outcomes were subsequently reported in the API batch.

### CPU shortage, cleanup and resource reuse

With `split-test` on CPU 1 and `cpu-only-test` on CPU 2, node 0 had only CPU 3
available for exclusive allocation. Node 1 had CPUs 6–7 available alongside
`memory-only-test` on CPU 5. An explicit two-CPU request on CPU node 0 with
memory node 1 was refused:

```text
Status: Failed
Reason: NUMAPlacementFailed
Message: Pod was rejected: NUMA node 0 has insufficient eligible CPUs or incompatible CPU topology
```

After deleting `cpu-only-test` and the refused request, the helper waited for
both manager checkpoints to drop the deleted UID and timed out at 90 seconds.
The follow-up captured this specific behavior for UID
`2ce36053-0b7a-40bd-804f-19af77557252`:

| Phase | CPU checkpoint | Memory checkpoint |
| --- | --- | --- |
| Before retry | Deleted UID absent | `app` retained 134217728 bytes on NUMA 0 |
| After next admission | Deleted UID absent | Deleted UID absent |

The identical replacement request then ran on **CPUs 2–3, memory node 1**.
The surviving split and memory-only Pods retained their original masks.
This supports resource reuse and cleanup observed on subsequent admission.
It does not establish immediate idle cleanup, repeated failed-admission
cleanup for every form, device accounting, or rollback after actual partial
allocation. The idle-cleanup expectation remains separately scoped for review;
the record does not change the feature contract to turn the timeout into a
blanket cleanup PASS. No manual checkpoint edits were prescribed or reported.

One follow-up parser failed on an empty/invalid `deleted-pod.json`; the user
recovered the UID from the valid earlier `cpu-only-test-pod.json`. That helper
error is not itself a Kubernetes placement failure.

### Pod-wide masks: 15 reported PASS

The complete supplied transcript reports five checks for each of three forms:

| Form | Expected CPU/memory nodes | During held init | After init release |
| --- | --- | --- | --- |
| Explicit | 0 / 1 | init, sidecar PASS | app-a, app-b, sidecar PASS |
| CPU-only | 0 / automatic 0 | init, sidecar PASS | app-a, app-b, sidecar PASS |
| Memory-only | automatic 1 / 1 | init, sidecar PASS | app-a, app-b, sidecar PASS |

These were mask checks. The batch did not measure init/sidecar page residency
or restart those containers. Each completed fixture was deleted before the
next form was created.

### Compatibility and API batch: 102 reported PASS, 0 FAIL, 0 NOT RUN

The user supplied the aggregate summary. The archived runner defines these
executed subcases:

- Equal-node, legacy `numaNode`, ordinary, explicit split, CPU-only and
  memory-only single-container masks before/after update checks, all under
  `independent-numa`. Ordinary Pods must have one observed common CPU/memory
  node; the runner does not assume node 0.
- API server dry-run rejection of -1/64 for all three placement fields,
  legacy/new-field mixing, fractional CPUs and unequal memory requests/limits
  in application, init and sidecar containers. Rejection text must match the
  feature-specific validation, rather than any nonzero exit being accepted.
- Server dry-run rejection of changing/removing existing placement fields
  and adding fields to suitable ordinary/partial Pods; actual unchanged-value
  and metadata annotation updates succeed, with masks rechecked.
- Feature-specific server dry-run rejection of CPU resize and ephemeral
  container addition on an explicit split Pod. A separate memory resize was
  not exercised.
- Real Pod admission refusal for absent CPU node 63 and absent memory node 63,
  requiring `NUMANodeNotFound` and the resource-specific absent-node message.

This does not test legacy/ordinary behavior under the old `single-numa-node`
policy, every invalid workload shape, or unsupported kubelet configurations.

### Private-anonymous residency: seven cases reported all passed

The user replied **all passed** to the seven-case residency runner. Individual
observation JSON and build/image identities remain to be imported from the VM.

| Form | CPU node | Memory node |
| --- | --- | --- |
| Explicit split | 0 | 1 |
| Reverse split | 1 | 0 |
| CPU-only | 0 | automatic 0 |
| Memory-only | automatic 1 | 1 |
| Equal | 0 | 0 |
| Legacy | 0 | 0 |
| Ordinary | Observed common node | Same observed node |

Each case used a 16MiB touched private-anonymous mapping. The existing probe
uses `move_pages` with NULL nodes and flags zero to query at most 4096 sampled
pages, without binding or migrating memory. The reported PASS covers those
queries at the observation instant. It does not cover all accessible memory,
hugepages, STREAM arrays, continuous residency, or residency after recovery.

### Kubelet recovery: 22 reported PASS, 0 FAIL

The supplied complete transcript confirms for the three single-application
forms:

- Baseline masks, a new kubelet process using the same configuration/root and
  existing checkpoints, retained Pod UIDs, and masks after recovery.
- Identical per-Pod CPU and memory checkpoint assignments before/after restart.
- Genuinely new container starts after recovery, preserving Pod identity and
  expected resource masks. Surviving old containers alone were not treated as
  sufficient evidence.

The replacement kubelet was managed by transient unit
`numa-ticket11-kubelet-recovery-j_t8qzp9.service`. The runner retained a stop
helper at `$RUN/stop-restarted-kubelet.sh`. Multi-container init/sidecar recovery,
conflicting checkpoints and post-recovery page residency remain untested.

## Coverage against the 34-category acceptance checklist

This table separates passing subchecks from remaining work. A covered subcheck
does not close an entire category whose remaining column is nonempty.

| Category | PASS subchecks reported | NOT RUN / remaining scope |
| --- | --- | --- |
| explicit | Single-app masks/residency; Pod-wide masks | Pod-wide residency and other lifecycle variants |
| cpu-only | Feasible common-node choice; masks/residency; Pod-wide masks | Non-common selection, Pod-wide residency and remaining lifecycle variants |
| memory-only | Feasible common-node choice; masks/residency; Pod-wide masks | Non-common selection, Pod-wide residency and remaining lifecycle variants |
| equal | Single-app masks/residency, updates | Multi-container/lifecycle variants |
| legacy | Single-app masks/residency, updates under new policy | Multi-container/lifecycle variants |
| ordinary | Single-app common-node masks/residency, metadata updates under new policy | Multi-container/recovery/cleanup comparison |
| hugepages | None | All sizes, confinement and residency; not provisioned |
| memory-only-node | None | No CPU-less NUMA node in this VM topology |
| container-restart | Single app in all three new forms; UID/restart/masks | Init/sidecar restart and residency after restart |
| kubelet-restart | Three single-app forms; checkpoints, UID and masks | Init/sidecar variants and residency after recovery |
| post-recovery-start | New app container in all three new forms; masks | New init/sidecar instances and residency |
| cpu-exhaustion | One explicit split two-CPU request refused without fallback | Other forms, repeated trials and complete capacity-artifact review |
| memory-exhaustion | None | Ordinary-memory shortage and whole-group cases |
| hugepage-exhaustion | None | Per-size and combined-size refusal; not provisioned |
| automatic-aggregate-only | None | Omitted resource fits only across multiple nodes |
| nearest-exhausted | None | Exhaust common/nearest node, choose farther feasible singleton |
| missing-id | CPU and memory node 63 rejected with specific reason | Raw per-case evidence review |
| cpuless-cpu-target | None | No CPU-less node available |
| invalid-api | All field ranges, mixed legacy/new fields; equal accepted | Raw per-case evidence review |
| ineligible | Fractional CPU and unequal memory in app/init/sidecar | Missing/unequal CPU forms, missing memory and Pod-level resource budgets |
| unsupported-config | None | Old policies/new fields, container scope, non-static managers, align-by-socket |
| immutable | Change/remove/add, matching unchanged values | Raw per-case evidence review |
| resize | Explicit Pod CPU resize dry-run refused | Memory resize and other placement variants |
| ephemeral | Explicit Pod addition dry-run refused | Other placement variants and raw evidence review |
| unrelated-update | Metadata annotation accepted, masks preserved across six forms | Raw per-case evidence review; broader variants |
| device-compatible | None | Topology-aware plugin/resource evidence not supplied |
| device-conflict | None | Topology-aware plugin/resource evidence not supplied |
| legacy-old-policy | None | Separate old-policy environment, multi-container and recovery |
| ordinary-old-policy | None | Separate old-policy comparison and lifecycle |
| failure-cleanup | Failed explicit request followed by successful retry | Failed-UID audit, repeated variants, devices and full counter comparison |
| normal-cleanup | Deleted CPU-only Pod released CPU; memory removed on next admission; surviving masks preserved | Idle-cleanup expectation audit, broader variants and full accounting review |
| partial-rollback | None | Actual partial allocation/fault injection and unrelated-state preservation |
| distance-data-refusal | None | Offline invalid-distance/fault-injection environment |
| recovery-conflict | None | Offline conflicting/partial checkpoint injection |

## Historical STREAM deferral and resume point (2026-10-19)

The operator reached STREAM setup, but `kubectl get node ubuntu-numa-test`
timed out before any confirmed image build or benchmark execution. The user
then reported likely closing the original cluster terminal. The foreground
launcher can stop the API server/etcd when it exits, while the separately
managed replacement kubelet can remain alive. This is a plausible operational
explanation; current process state and control-plane recovery were not verified.
It is not recorded as a NUMA workload correctness failure.

The user explicitly requested deferring STREAM and recording progress.
**STREAM remains NOT RUN:** no confirmed workload image build/import, LLC
discovery, warm-up, paired execution, numerical validation, array-residency
evidence or bandwidth measurement. No speedup, DDR/HBM mapping or throughput
claim is made. The image-setup helper is retained as prepared tooling only;
its proposed hostname correction has not been confirmed applied.

For the next session:

1. Preserve the existing run, certificates, service-account key, etcd data,
   kubelet root and all raw evidence. Inspect which services remain alive;
   restore the control plane with those same identities/state rather than
   deleting checkpoints or using a fresh run as evidence of recovery.
2. Obtain the complete raw VM archives and effective configuration; keep
   private keys and admin kubeconfigs out of shared evidence exports.
3. Complete image setup, establish actual guest cache visibility and per-node
   capacities, and select an honestly supported STREAM mode. Cache metadata
   has not been observed, so bandwidth sizing is not yet established.
4. Run the paired procedure with matched CPU allocation, image/build, arrays
   and thread counts, retaining actual STREAM-array residency and raw output.
   The intended VM comparison is CPU node 0 with memory nodes 0 versus 1;
   actual sizing/thread sweep is still to be configured.
5. Finish or explicitly account for every remaining matrix scope before
   resolving ticket 11. Real physical DDR/HBM evaluation remains outstanding.

The earlier 2026-10-13 manual VM-test acknowledgment and older dated execution
records remain unchanged. These newly reported VM results supplement them.

## Validation of this progress update

Only documentation and evidence snapshots were added/updated. Local checks
validate JSON readability, snapshot hashes, relative Markdown links, the
34-category table and whitespace. No VM commands, benchmarks, Kubernetes test
suites, cluster recovery or process shutdown were executed for this update.
