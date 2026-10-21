# 11: Record user-operated VM or hardware acceptance evidence

**What to build:** Produce the second milestone's dated runtime evidence using the user's chosen VM or team-provided hardware, with conclusions limited to the environment and cases actually exercised.

**Blocked by:** 10 — Automate paired STREAM evaluation and evidence reporting.

**Status:** claimed

**Progress:** Updated 2026-10-21: STREAM VM smoke independently assessed from copied raw artifacts, PASS_SMOKE (36/36). Earlier runtime-matrix evidence review and remaining cases are still open.

**Type:** task

**External prerequisite:** The user selects and makes a suitable environment available, or supplies outputs from their own manual run. This ticket does not block the software/tooling milestone.

- [ ] Record fork version, configuration, reservations, topology/distances, VM/host boundary, runtime, image identity, and exact commands or user-supplied evidence.
- [ ] Execute or assess the runtime placement, failure, cleanup, update, and restart matrix. Record PASS/FAIL/NOT RUN per case and explain missing hugepage/device/topology capabilities.
- [ ] Run paired STREAM experiments where supported and collect the required control and residency evidence; record unsupported experiments as NOT RUN rather than substituting invented results.
- [x] Label virtual-topology findings accurately. Claim real DDR/HBM throughput only after verifying hardware and mapping; no speedup threshold is required.
- [ ] Treat observed correctness failures as unresolved work and link follow-up defects; do not repair evidence by broadening the contract or silently altering checkpoints.
- [x] Retain the earlier manual VM-test acknowledgment and historical records. Distinguish newly reproduced results from unverified historical observations.
- [ ] Close the evidence task only with an explicit account of what was validated and what remains untested; report outstanding real-HBM evaluation if the user chooses VM-only validation.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 11 of 11. This is an operator-assisted follow-up, not a prerequisite for an agent to finish implementing the feature and runnable tooling. Actual access and execution may be deferred by the user.

### Progress recorded 2026-10-19

The user operated an ARM64 QEMU/HVF Ubuntu VM, node `ubuntu-numa-test`,
from fork commit `b76f73a6f478ef3ed63cdf58bcb295dfcec1013b` on
`numa_support_hbm`. The guest exposes CPUs 0–3 on NUMA 0 and 4–7 on NUMA 1.
The run directory is `/home/jukeliu/numa-ticket11.5WO3NVwh`.

Completed **within the tested scopes**, based on user-supplied output:

- [x] Explicit split, CPU-only and memory-only single-container masks.
- [x] Single-container restarts for all three forms; same Pod UID and preserved placement.
- [x] Explicit CPU-shortage refusal on NUMA 0 despite spare CPUs on NUMA 1; successful identical request after releasing resources; surviving Pods retain masks.
- [x] Pod-wide masks: 15 checks covering init/sidecar and two applications/sidecar for all three forms.
- [x] Compatibility/API batch: user reports `PASS: 102, FAIL: 0, NOT RUN: 0`. Includes equal/legacy/ordinary Pods under `independent-numa`, invalid API/resource forms, placement immutability, metadata updates, resize/ephemeral rejection and absent-node refusal. This is a count of runner checks, not 102 acceptance categories.
- [x] Private-anonymous page-residency batch: user reports all seven cases passed, including both split directions, partial forms, equal, legacy and ordinary Pods. Scope is the probe's touched/sampled mapping at observation time.
- [x] Kubelet recovery: 22 reported passes covering retained Pod identity, CPU/memory checkpoint assignments, masks and genuinely new container starts for the three single-application forms.
- [ ] Import/review the complete raw VM archives, effective configuration, resolved image IDs and probe observations; several batch results were supplied only as summaries.
- [ ] Complete or explicitly account for untested matrix variants, including memory/hugepage exhaustion, automatic-placement edge cases, init/sidecar recovery, old-policy compatibility, devices and fault injection.
- [ ] Run and assess paired STREAM. No bandwidth result is available.

The initial release/retry script timed out waiting for every deleted-Pod
checkpoint entry to disappear while idle. The supplied follow-up showed the
CPU entry already absent, the memory entry retained before retry, and both
absent after admission of the replacement. The replacement received CPUs 2–3
and memory node 1; the surviving Pods retained CPUs 1 and 5. This records
observed cleanup on subsequent admission, not immediate idle cleanup or proof
of rollback after partial allocation. No checkpoint edits were prescribed or
reported.

STREAM setup stopped at an API request timeout. The user subsequently reported
closing the original cluster terminal, consistent with loss of its control
plane while the separately managed kubelet could remain running. Recovery
was not executed or verified. The user then explicitly deferred STREAM.
Image build/import, guest LLC discovery, STREAM execution and measurements
remain **NOT RUN / unconfirmed**. Existing test results and historical records
are retained; ticket 11 is not resolved.

Detailed scopes, evidence locations and the remaining matrix are in
[verification-11.md](../verification-11.md). Input captures and the provided
operator scripts are retained in the
[dated evidence record](../runtime/evidence/2026-10-19/README.md).

### STREAM raw evidence assessed 2026-10-21

The fresh run `numa-ticket11.ogQweit2` produced `stream-smoke.nnftze4f`.
All 2,424 listed artifact hashes match; re-evaluation reproduces PASS_SMOKE
exactly, with all 36 executions passing. CPU allocation remains 1–3 on node 0;
array pages match memory node 0 or 1 as requested. All 1,954 base pages of
each array were queried before timing (4KiB base pages; 108 array records).
Numerical/build/affinity checks and all archive/deletion commands passed.

This supersedes the historical STREAM deferral below the October 19 entry.
Qualified bandwidth remains NOT RUN because cache sizes are unknown. Physical
DDR/HBM evaluation remains NOT RUN. The deployed binary revision is still
b76f73a6f478ef3ed63cdf58bcb295dfcec1013b, not subsequent source corrections.
The STREAM smoke subcase is complete; the broader acceptance checkbox remains
open pending an explicit disposition of unsupported performance work and the
remaining runtime matrix. Prior aggregate-only batches still need raw review.

See the [reviewed evidence](../runtime/evidence/2026-10-21/README.md).

### Remaining acceptance audited 2026-10-21

The [34-category disposition](../remaining-acceptance-11.md) distinguishes raw
review, VM subcases, setup/topology requirements, separate configurations and
isolated fault injection. All 15 historical snapshot hashes match, but original
October 19 raw batches await import. No new cases are waived or declared PASS.
