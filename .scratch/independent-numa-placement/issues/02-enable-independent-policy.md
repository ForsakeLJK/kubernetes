# 02: Enable independent-numa policy for existing Pod forms

**What to build:** An operator can select the new independent-numa policy and continue running ordinary Pods and legacy numaNode Pods with their existing common-node behavior and guarantees.

**Blocked by:** 01 — Prepare existing placement enforcement for separate resource targets.

**Status:** resolved

**Type:** task

- [x] Register independent-numa throughout policy selection, configuration validation, operator help, and policy documentation.
- [x] Support Linux with pod scope; reject container scope clearly without changing global defaults. Participating Pods retain the required static CPU and memory manager configuration.
- [x] Ordinary Pods delegate to existing single-numa-node behavior without inheriting new participating-Pod eligibility restrictions.
- [x] Legacy numaNode Pods work under either supported policy and retain exact CPU/memory placement, refusal, rollback, and recovery behavior.
- [x] Preserve other policies, existing topology node-count limits, and supported option behavior. Reject incompatible align-by-socket configuration under the new policy.
- [x] Use the prepared resource-specific access with legacy shared-target fallback; exercise allocation and runtime-mask generation, not only policy-name registration.
- [x] Report common-node alignment truthfully without declaring every future independent-numa outcome globally aligned.
- [x] Verify operator configuration through admission, allocation, restart validation, and masks for ordinary and legacy forms, including unsupported configuration and capacity failures.
- [x] Record focused regression results and update the terminology/documentation to distinguish legacy combined placement from the new policy's eventual independent placement.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 02 of 11. New API fields and split placement arrive in ticket 03; enabling this policy already has a complete compatibility behavior.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14. Operators can select
`independent-numa` on Linux with pod scope. Ordinary Pods retain single-node
merging and existing eligibility, while legacy `numaNode` Pods retain combined
hard placement under either supported policy. Both static managers remain
required for participating Pods. Defaults, supported options, and topology
node-count limits are preserved; `align-by-socket` is rejected.

The prepared resource-specific affinity, allocation, rollback, recovery, and
runtime-mask paths are exercised with real managers and checkpoints. Alignment
metrics compare concrete CPU and memory locality rather than shared hints, so
fractional shared CPUs or disabled Memory Manager cannot produce false alignment
successes. Documentation and generated OpenAPI policy help are updated.
No new Pod fields or split-target planner were added.

Focused package tests and allocation/mask tests pass with race detection;
all 11 platform typechecks, vet, Linux cross-compilation, repository generators,
formatting, and changed-file boilerplate checks pass. The full suite reports
144 failures in 11 package groups, all observed in the ticket-01 run; the changed
packages and subsequent vendor/tool suites pass. Standards and Spec reviews have
zero remaining findings. Linux runtime enforcement was not executed.
See [verification-02.md](../verification-02.md) for commands, scope, evidence,
review correction, generated-output handling, and environmental limits.
