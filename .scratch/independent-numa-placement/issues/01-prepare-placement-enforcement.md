# 01: Prepare existing placement enforcement for separate resource targets

**What to build:** Preserve every existing legacy NUMA placement behavior while preparing internal placement, affinity, and failure cleanup to carry separate CPU and memory targets. This is a behavior-preserving prefactor, verified through existing successful, refused, and restarted Pods.

**Blocked by:** None (can start immediately).

**Status:** resolved

**Type:** task

- [x] Represent resolved CPU and memory targets separately from whether a Pod has a placement requirement. Legacy requirements resolve to the same target for both resources; ordinary Pods retain their existing path.
- [x] Introduce resource-specific memory affinity access alongside existing CPU/device affinity, with existing shared-affinity fallback and complete cleanup. Avoid breaking every existing store caller at once.
- [x] Make allocation and pre-start validation consume resolved targets rather than assuming a single API field can always be dereferenced.
- [x] Generalize memory rollback to changes in all affected nodes, ordinary memory, hugepages, and init reuse. Preserve unrelated and pre-existing allocations; do not restore an obsolete global state snapshot.
- [x] Preserve existing CPU/device rollback, checkpoint validation, immutable placement, resize/debugging exclusions, and runtime masks.
- [x] Do not add public fields or policy behavior in this prefactor. Legacy and no-field Pods must have identical observable behavior before and after it.
- [x] Cover success, resource shortage, injected partial failure, cleanup, and valid/conflicting recovery with focused tests; include shared-affinity fallback tests.
- [x] Record verification commands/results and environmental limits. Leave generated files to repository tooling.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 01 of 11. This is the permitted prefactoring exception to vertical slicing. Subsequent tickets use the prepared enforcement path; it does not itself enable split placement.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14. Legacy requirements now resolve
separate internal CPU and memory targets while ordinary Pods retain their path.
Optional memory affinity preserves existing Store callers and cleans up both
resource views. Allocation and pre-start checks consume resolved targets;
memory rollback preserves existing and unrelated allocations across every
affected node, ordinary memory, hugepages, and init reuse. No public API fields,
policy behavior, module files, or generated artifacts were changed.

Focused and full manager tests with race detection, Go vet, all 11 platform
typechecks, Linux container-manager cross-compilation, formatting, and changed
file boilerplate checks passed. Standards and specification reviews found no
substantive issues. The repository-wide run completed with 151 failures in 12
package groups; baseline reproductions, platform limits, a Finder artifact, and
a timing-sensitive assertion are documented in [the verification record](../verification-01.md).
Linux runtime validation was not executed. Existing untracked planning files
outside this ticket's tracker updates were preserved.
