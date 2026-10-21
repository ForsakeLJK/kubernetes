# Operator evidence snapshot: 2026-10-19

This directory records ticket 11's user-operated QEMU VM progress. The
[verification record](../../../verification-11.md) gives tested scopes,
limitations and the deferred STREAM resume point.

- `inputs/`: exact copies of the four environment files supplied by the user:
  topology, initial cluster baseline, setup inventory and binary/runtime readiness.
- `operator-kit/`: selected local scripts and build metadata provided during
  this conversation, snapshotted when progress was recorded. These are not
  independently hashed copies retrieved from the VM. The scripts are retained
  for command/procedure traceability; they should not be run blindly as a
  cluster-resume procedure. Temporary editor files, caches, images and binaries
  are excluded.
- `reported-results.json`: structured transcription of user messages, including
  reported PASS results, exact returned masks/refusal/checkpoint observations,
  known VM evidence paths and operational interruptions. It is not an original
  runtime report or a substitute for missing raw files.
- `snapshot-sha256.json`: SHA-256 of the captured inputs, operator-kit files and
  structured transcription. These hashes identify this local snapshot; they do
  not certify the completeness or authenticity of the unimported VM archives.

The user-supplied VM run is `/home/jukeliu/numa-ticket11.5WO3NVwh`.
Known artifacts there include:

| Location relative to run | Contents / supplied evidence |
| --- | --- |
| `split-test-masks.json`, `cpu-only-test-masks.json`, `memory-only-test-masks.json` | Full mask results pasted in conversation |
| `container-restart.9DmQmt/` | Before/after Pod identity and mask records; transcript supplied |
| `cpu-shortage.8AXMe5/` | Explicit two-CPU refusal, manifest and checkpoints; describe output supplied |
| `cpu-release-retry.*/` | Release/retry checkpoint captures and mask results; exact directory suffix not supplied |
| `pod-wide.oRECj4/` | 15 mask checks; complete batch transcript supplied |
| `compatibility-api.*/` | `results.json`, manifests and command captures; aggregate 102-pass summary supplied |
| `page-residency.*/` | Probe build, image/Pod identities, observations and results; user reported all seven cases passed |
| `kubelet-recovery.j_t8qzp9/` | Checkpoints, identity/masks before/after recovery and new container starts; complete 22-pass transcript supplied |
| `stream-setup.*/` | Setup attempt; API request timed out; build/import and benchmarks unconfirmed |

Globs are search locators, not invented exact archive names. The complete VM
archives have not been copied into this directory. Raw effective configuration,
resolved workload image identities, observation samples, source/build hashes and
per-command captures still need review before a final acceptance account.
Do not export the run's private keys or admin kubeconfigs with public evidence.

STREAM is deferred at the user's request. No throughput result is included.
