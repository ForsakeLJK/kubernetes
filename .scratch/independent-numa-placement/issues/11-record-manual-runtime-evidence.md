# 11: Record user-operated VM or hardware acceptance evidence

**What to build:** Produce the second milestone's dated runtime evidence using the user's chosen VM or team-provided hardware, with conclusions limited to the environment and cases actually exercised.

**Blocked by:** 10 — Automate paired STREAM evaluation and evidence reporting.

**Status:** ready-for-agent

**Type:** task

**External prerequisite:** The user selects and makes a suitable environment available, or supplies outputs from their own manual run. This ticket does not block the software/tooling milestone.

- [ ] Record fork version, configuration, reservations, topology/distances, VM/host boundary, runtime, image identity, and exact commands or user-supplied evidence.
- [ ] Execute or assess the runtime placement, failure, cleanup, update, and restart matrix. Record PASS/FAIL/NOT RUN per case and explain missing hugepage/device/topology capabilities.
- [ ] Run paired STREAM experiments where supported and collect the required control and residency evidence; record unsupported experiments as NOT RUN rather than substituting invented results.
- [ ] Label virtual-topology findings accurately. Claim real DDR/HBM throughput only after verifying hardware and mapping; no speedup threshold is required.
- [ ] Treat observed correctness failures as unresolved work and link follow-up defects; do not repair evidence by broadening the contract or silently altering checkpoints.
- [ ] Retain the earlier manual VM-test acknowledgment and historical records. Distinguish newly reproduced results from unverified historical observations.
- [ ] Close the evidence task only with an explicit account of what was validated and what remains untested; report outstanding real-HBM evaluation if the user chooses VM-only validation.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 11 of 11. This is an operator-assisted follow-up, not a prerequisite for an agent to finish implementing the feature and runnable tooling. Actual access and execution may be deferred by the user.
