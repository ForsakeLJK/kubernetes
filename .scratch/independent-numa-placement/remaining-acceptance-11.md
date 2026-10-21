# Ticket 11 remaining acceptance audit — 2026-10-21

Ticket remains claimed. This scope audit does not mark untested cases PASS or
waive them. STREAM smoke is verified separately from the 34 runtime categories.

## Evidence available

All 15 October 19 snapshot hashes match. That snapshot contains environment
inputs, local script snapshots and transcribed user results. Original VM batch
archives have not been copied locally; reported 15/102/7/22 passes remain scoped
operator reports pending raw review. The copied October 21 STREAM archive is
independently verified PASS_SMOKE, with all 2,424 listed hashes matching.

Both runs use b76f73a6f478ef3ed63cdf58bcb295dfcec1013b. They do not validate
subsequent source corrections. Matching rebuilt binaries and provenance are
needed before claiming runtime validation of those corrections.

## Dispositions

- REVIEW + VM: import old artifacts first; then execute remaining supported
  subcases, checking per-node capacity. No additional deferral is assumed.
- SETUP REQUIRED: hugepages are not provisioned; this is a setup gap, not proof
  QEMU cannot support them.
- TOPOLOGY CHANGE: the current two nodes both have CPUs; a CPU-less guest node
  requires a different topology.
- DEVICE SETUP: no suitable topology-aware device plugin evidence exists.
- SEPARATE CONFIGURATION: use isolated policy/manager configurations instead
  of changing the active cluster in place.
- ISOLATED FAULT INJECTION: use disposable state, never historical checkpoints.

These remain required for full matrix coverage. A bounded VM-only conclusion
must explicitly account for untested cases; it must not claim all categories
passed. Two-node automatic-selection tests need a feasibility check; richer
ranking/tie scenarios may need a richer topology.

| Category | Existing operator-reported scope | Disposition | Remaining scope |
| --- | --- | --- | --- |
| explicit | Single-app masks/residency; Pod-wide masks | REVIEW + VM | Pod-wide residency and other lifecycle variants |
| cpu-only | Feasible common-node choice; masks/residency; Pod-wide masks | REVIEW + VM | Non-common selection, Pod-wide residency and remaining lifecycle variants |
| memory-only | Feasible common-node choice; masks/residency; Pod-wide masks | REVIEW + VM | Non-common selection, Pod-wide residency and remaining lifecycle variants |
| equal | Single-app masks/residency, updates | REVIEW + VM | Multi-container/lifecycle variants |
| legacy | Single-app masks/residency, updates under new policy | REVIEW + VM | Multi-container/lifecycle variants |
| ordinary | Single-app common-node masks/residency, metadata updates under new policy | REVIEW + VM | Multi-container/recovery/cleanup comparison |
| hugepages | None | SETUP REQUIRED | All sizes, confinement and residency; not provisioned |
| memory-only-node | None | TOPOLOGY CHANGE | No CPU-less NUMA node in this VM topology |
| container-restart | Single app in all three new forms; UID/restart/masks | REVIEW + VM | Init/sidecar restart and residency after restart |
| kubelet-restart | Three single-app forms; checkpoints, UID and masks | REVIEW + VM | Init/sidecar variants and residency after recovery |
| post-recovery-start | New app container in all three new forms; masks | REVIEW + VM | New init/sidecar instances and residency |
| cpu-exhaustion | One explicit split two-CPU request refused without fallback | REVIEW + VM | Other forms, repeated trials and complete capacity-artifact review |
| memory-exhaustion | None | REVIEW + VM | Ordinary-memory shortage and whole-group cases |
| hugepage-exhaustion | None | SETUP REQUIRED | Per-size and combined-size refusal; not provisioned |
| automatic-aggregate-only | None | REVIEW + VM | Omitted resource fits only across multiple nodes |
| nearest-exhausted | None | REVIEW + VM | Exhaust common/nearest node, choose farther feasible singleton |
| missing-id | CPU and memory node 63 rejected with specific reason | REVIEW + VM | Raw per-case evidence review |
| cpuless-cpu-target | None | TOPOLOGY CHANGE | No CPU-less node available |
| invalid-api | All field ranges, mixed legacy/new fields; equal accepted | REVIEW + VM | Raw per-case evidence review |
| ineligible | Fractional CPU and unequal memory in app/init/sidecar | REVIEW + VM | Missing/unequal CPU forms, missing memory and Pod-level resource budgets |
| unsupported-config | None | SEPARATE CONFIGURATION | Old policies/new fields, container scope, non-static managers, align-by-socket |
| immutable | Change/remove/add, matching unchanged values | REVIEW + VM | Raw per-case evidence review |
| resize | Explicit Pod CPU resize dry-run refused | REVIEW + VM | Memory resize and other placement variants |
| ephemeral | Explicit Pod addition dry-run refused | REVIEW + VM | Other placement variants and raw evidence review |
| unrelated-update | Metadata annotation accepted, masks preserved across six forms | REVIEW + VM | Raw per-case evidence review; broader variants |
| device-compatible | None | DEVICE SETUP | Topology-aware plugin/resource evidence not supplied |
| device-conflict | None | DEVICE SETUP | Topology-aware plugin/resource evidence not supplied |
| legacy-old-policy | None | SEPARATE CONFIGURATION | Separate old-policy environment, multi-container and recovery |
| ordinary-old-policy | None | SEPARATE CONFIGURATION | Separate old-policy comparison and lifecycle |
| failure-cleanup | Failed explicit request followed by successful retry | REVIEW + VM | Failed-UID audit, repeated variants, devices and full counter comparison |
| normal-cleanup | Deleted CPU-only Pod released CPU; memory removed on next admission; surviving masks preserved | REVIEW + VM | Idle-cleanup expectation audit, broader variants and full accounting review |
| partial-rollback | None | ISOLATED FAULT INJECTION | Actual partial allocation/fault injection and unrelated-state preservation |
| distance-data-refusal | None | ISOLATED FAULT INJECTION | Offline invalid-distance/fault-injection environment |
| recovery-conflict | None | ISOLATED FAULT INJECTION | Offline conflicting/partial checkpoint injection |

## STREAM and performance

STREAM placement/workflow is verified PASS_SMOKE for the tested pair and thread
sweep. All 1,954 base pages per array were queried at the pre-timing point.
Qualified bandwidth remains NOT RUN: guest LLC size is unknown. Larger arrays
alone do not establish cache-size qualification. DDR/HBM remains NOT RUN:
physical memory types and guest-to-host mapping are unverified.

## Next actions

1. Import validation artifacts from `/home/jukeliu/numa-ticket11.5WO3NVwh`.
2. Review masks, probe residency, API results, image identities, recovery and
   cleanup; preserve any observed failures as unresolved work.
3. Decide the deployed revision for remaining tests; use rebuilt/provenance-
   checked binaries if validating the later corrections.
4. Fill supported ordinary-memory, automatic-selection, lifecycle, API and
   cleanup gaps. Use isolated environments for the other groups, or record
   explicit NOT RUN dispositions in the final bounded acceptance account.

No original mutation scripts should be replayed against the fresh run without
checking their identity and prerequisites. The export helper only reads files.
