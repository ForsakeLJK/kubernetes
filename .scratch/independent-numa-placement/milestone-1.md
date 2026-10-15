# Milestone 1 readiness

Date: 2026-10-15. Branch: `numa_support_hbm`.

Software and tooling for tickets 01–10 are delivered. This is readiness for
operator runtime validation; no new live Kubernetes, NUMA or DDR/HBM result
is claimed. Ticket 11 owns that evidence.

| Area | Actual verification | Remaining runtime limitation |
| --- | --- | --- |
| API/policy/planning | Tickets 01–05 record validation, sparse/CPU-less nodes, directed distances, concrete allocation, rollback and recovery checks. | Matching API server/kubelet deployment and guest/hardware behavior. |
| Pod-wide enforcement/hugepages | Tickets 06–07 record application/init/sidecar allocation, lifetime accounting, recovery, device coordination and separate Linux runtime masks. | Linux runtime restarts, hugetlb provisioning and device-host observations. |
| Reproducible STREAM variant | Ticket 08 pins source/compiler image and records native serial build/run/numerical smoke, parser failures and observation metadata. Its 14 CLI tests pass again for ticket 10. | Linux/OpenMP image execution and live page-query availability. |
| Placement validation kit | Ticket 09 supplies full placement/lifecycle/refusal/accounting matrix, masks, private anonymous/hugetlb probe, command capture and honest reporting. Its 12 CLI tests pass again for ticket 10. | Operator execution of the matrix on a Linux VM or hardware. |
| Paired evaluation | Ticket 10 supplies explicit manifests, controlled sizing/sweep, alternating warm-ups and repeated runs, actual CPU/build matching, array-residency checks and median/range reports. All 16 CLI tests pass, including fake-cluster orchestration; Python typechecking passes. | Actual sequential CPU reuse, matched fork deployment, page residency and bandwidth measurements. |

Evidence: [01](verification-01.md), [02](verification-02.md),
[03](verification-03.md), [04](verification-04.md), [05](verification-05.md),
[06](verification-06.md), [07](verification-07.md), [08](verification-08.md),
[09](verification-09.md), [10](verification-10.md).

Earlier Kubernetes repository-wide runs have recorded failures; ticket 07
reports 143 failures matching previously recorded cases. This is not a claim
of a green Kubernetes-wide suite. Ticket 10 changes only Python tooling and
documentation; all 42 relevant evaluation/STREAM/runtime tests pass, including
real native serial STREAM smoke. No Go, module or generated files changed,
so Go suites and generators were not rerun for this ticket.

The current agent environment is macOS arm64. `podman machine list --format
json` returns `[]`; no Linux container VM is configured. Fake kubectl tests
and synthetic page/rate fixtures establish software behavior only. Native
smoke lacks Linux affinity and residency. No throughput from those checks is
reported as a placement comparison.

The user's earlier manual VM test of the previous feature is preserved as a
user report without inventing commands or outputs. The old dated runtime
record remains unchanged. VM results collected in ticket 11 will be VM
measurements. Physical DDR/HBM labels require operator-verified backing
hardware and node-mapping evidence. No required speedup threshold applies.
