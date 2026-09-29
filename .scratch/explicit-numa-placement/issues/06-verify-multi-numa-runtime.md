# 06: Verify the complete contract on a multi-NUMA Linux host

**What to build:** Provide repeatable Linux runtime acceptance tests, setup instructions, and recorded results demonstrating the complete NUMA placement contract on a machine exposing at least two NUMA nodes.

**Blocked by:** 05 — Preserve placement through container and kubelet restarts.

**Status:** claimed

**Type:** task

- [ ] Document setup using this fork's API server and kubelet, the required manager policies/scope, a runtime enforcing CPU/memory masks, at least two NUMA nodes, and appropriate reserved/allocatable ordinary memory and hugepage capacity.
- [ ] Explain that the operator arranges suitable machine selection; the scheduler is unchanged and an unsuitable assignment can fail at kubelet admission.
- [ ] Run successful placements selecting NUMA 0 and another existing node. Verify actual container CPU masks contain only CPUs from the selected node and actual memory-node masks contain exactly that node.
- [ ] Cover multiple application containers, init containers inspected while running, sidecars, ordinary memory, and requested hugepage sizes. Admission success or selected hints alone are not sufficient evidence.
- [ ] Recheck actual masks after container and kubelet restarts.
- [ ] Exhaust eligible CPUs, ordinary memory, and requested hugepage capacity on the requested node while another node has spare capacity; verify refusal and actionable status/events.
- [ ] Exercise invalid IDs, unsupported configurations, ineligible declarations, immutable placement updates, resize, and ephemeral-container additions through the relevant API/kubelet paths.
- [ ] Verify failed attempts and normal Pod cleanup leave no leaked allocations. Cover existing device-policy interaction without claiming a new device-locality guarantee.
- [ ] Run equivalent omitted-field Pods and confirm compatibility; include relevant automated regression results from the preceding slices.
- [ ] Record reproducible commands/procedures and results, separating automated results from host-dependent runtime evidence. Identify unavailable prerequisites and unexecuted tests explicitly; do not report skipped host tests as passing or claim complete runtime acceptance without evidence.
- [ ] State the limits of the guarantee: affinity and memory-allocation confinement, not physical locality of every accessible shared/cached page or a demonstrated application performance improvement.

## Comments

Approved ticket breakdown: slice 06 of 06. Ticket 05 transitively depends on all preceding implementation slices. Real multi-NUMA runtime evidence is required for complete acceptance.

2026-09-29: Added [Linux runtime procedure](../runtime/README.md), Pod fixtures,
an effective-mask checker, and a [results record](../runtime/results.md). The
local macOS workspace has no multi-NUMA Linux node, fork API server/kubelet
deployment, or CRI runtime. Host-dependent cases remain unexecuted, so this
ticket remains claimed rather than resolved.
