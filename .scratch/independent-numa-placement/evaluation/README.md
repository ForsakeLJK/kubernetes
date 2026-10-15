# Paired STREAM evaluation

Ticket 10 connects the [pinned STREAM variant](../stream/README.md) to the
completed `independent-numa` policy. It generates explicit `cpuNUMA`/`memNUMA`
Pods, executes paired cases sequentially, and checks saved evidence before
reporting independent-execution median and range. It uses the workload's
untimed array/worker collectors, the runtime kit's command capture, and the
shared strict CPU/memory mask parser. Python 3.12+ and standard-library modules
are sufficient on the operator machine.

## Prepare the environment and manifests

Deploy the matching fork API server and Linux kubelet. Configure static CPU
and Memory Managers, `topologyManagerPolicy: independent-numa`, pod scope,
and appropriate CPU/memory reservations as described in the
[runtime procedure](../runtime/README.md). The operator needs permission to
read node identity and kubelet `configz` through node proxy, and create/get/
exec/log/delete Pods and read events in an existing namespace. The runner
retains and checks actual `configz`; it does not change policy or reservations.

Build/push the [STREAM image](../stream/README.md#source-build-and-license),
then resolve its immutable registry digest. The image needs Python, GCC with
OpenMP, and `tar` (all in the pinned base). The workload image uses the exact
collector versions in this checkout. Page-location queries must be permitted
by the container runtime/kernel. A denied `move_pages` query prevents a
controlled comparison; no memory-binding override is supplied by the runner.

Copy `config.example.json` to a machine-specific file. Replace the image,
node and CPU/memory IDs, capacity, cache and environment inputs; example sizes
are not discovered hardware facts. Cases `a` and `b` follow `memory_nodes`
order. Keep neutral names until backing memory types have been verified.

The allocated whole CPU count stays constant across the thread sweep. Include
one thread and available multi-thread counts; every count must fit `cpu`.
Every Pod requests and limits exactly the same CPU and memory quantities.
Each double array must have at least a million elements and occupy at least
four times the relevant LLC sum. Set `llc_bytes` to a conservative sum covering
the CPUs eligible for this experiment. The collector also reads actual
allocated-CPU LLCs and refuses an insufficient budget. Three arrays consume
`24 * array_size` bytes; add `overhead_bytes` for compilation, Python/OpenMP
and runtime state. This total must fit the container limit and both supplied
per-node available capacities. Establish those capacities from actual topology,
reservations and current load. Planned capacities reserve no resources;
kubelet admission and OOM evidence remain authoritative.

Record `environment.interference`, `settings`, deployed `fork_commit`, and
supporting topology/host/configuration files in `environment.files`. File paths
are relative to the config file. Preparation copies them into the archive.
For a physical DDR/HBM comparison add this optional mapping, with actual
verified nodes and retained backing hardware/mapping evidence:

```json
"backing": [
  {"type":"DDR", "node":0, "evidence":"hardware-mapping.txt", "verified_by":"operator and date"},
  {"type":"HBM", "node":3, "evidence":"hardware-mapping.txt", "verified_by":"operator and date"}
]
```

Put `hardware-mapping.txt` in `environment.files`. It must document actual
memory types and their node mapping using platform/firmware/vendor evidence;
a nonempty file or attestation alone cannot establish hardware truth. The
report's DDR/HBM label records **operator verification** backed by that file,
not automatic hardware detection. Missing/empty evidence, mismatched node IDs
or absent verifier keeps neutral hardware NUMA labels. `environment.kind: vm`
always produces VM measurements, even if the mapping claims DDR/HBM.

From this directory:

```sh
python3 paired.py prepare --config /path/to/host.json --output /path/to/new-evaluation
```

`plan.json` records all settings and the complete schedule. `manifests/*.json`
are Kubernetes manifests accepted by kubectl. Inspect them before execution.
Preparation makes no cluster calls and never overwrites an archive. Change
configuration by preparing another directory; the runner rejects edited
manifests or a shortened/inconsistent schedule.

## Execute and retain evidence

```sh
python3 paired.py run --output /path/to/new-evaluation
# Optional: --kubectl /absolute/path/to/kubectl
python3 paired.py report --output /path/to/new-evaluation
```

For each thread count, the schedule runs one independent warm-up per case,
then at least five measured executions per case. Pair order alternates each
round. Each execution creates a fresh Pod and recompiles in the same in-Pod
path with the same flags, then retrieves evidence and deletes its own Pod
with a completion wait. The collector remains alive after STREAM completes
so evidence can be retrieved from `emptyDir` before deletion. Runtime refusal,
timeouts, retrieval failures and cleanup errors stop the sweep and retain
commands/status/events/logs; unexecuted rows remain NOT RUN. Interruptions
may leave a Pod; inspect its recorded name and archive before cleanup.
Runs cannot resume into an existing `executions` directory.

Two concurrent Guaranteed Pods would receive different dedicated CPU sets.
Sequential creation allows reuse but does not guarantee it. Every warm-up and
measured execution must have the same observed allocation, binary, build
inputs, image and LLC. Workers at each equal thread count must use the same
CPU mapping. A fixed CPU NUMA ID alone is insufficient. CPU allocation or build
changes make the comparison FAIL; use a quiet dedicated node and repeat the
entire experiment in a new archive after investigating.

Each `executions/ID/` retains raw shared command-capture directories, Pod state,
events and logs, orchestration outcome, retrieved tar output and extracted
`workload/`. The latter contains inherited effective masks, CPU-node topology,
actual LLC metadata, workload exit status and the standalone `run/` artifacts:
raw stdout/stderr, compiler logs, actual binary/source and build hashes, live
array addresses, worker CPUs/masks, raw proc files and sampled array residency.
No observer executes application memory-binding or migration commands.

`report.json` and `report.txt` preserve per-execution outcomes and report
Copy/Scale/Add/Triad **decimal MB/s**. These are medians and ranges of STREAM's
best rate in each independent measured execution; warm-ups are excluded.
No conversion is applied. Decimal GB/s would be MB/s divided by 1000.
Artifacts have SHA-256 entries. Keep the complete archive for review.

Numerical failure, OOM, nonzero exit, incomplete execution, wrong effective masks,
wrong/missing array residency, oversubscribed/unpinned workers and uncontrolled
CPU/build changes prevent a successful controlled comparison. Raw output is
reparsed, saved source/binary hashes are checked and array sample counts are
correlated with their actual address ranges. Residency is sampled after
initialization and before calibration/timed kernels; it proves those samples
at that instant. It does not prove all-page residency throughout execution.
See the workload's [comparability caveats](../stream/README.md#comparability-and-results).
There is no required HBM speedup threshold.

Statuses are `PASS` (complete live bandwidth comparison), `PASS_SMOKE` (complete
live small/workflow smoke), `PASS_FIXTURE` (explicit synthetic inputs), `FAIL`,
and `NOT RUN`. Only `PASS` sets `successful_controlled_comparison: true`.
Classification distinguishes `vm_measurement`, `hardware_numa_measurement`,
`verified_ddr_hbm` and `synthetic_fixture`. Summaries from an incomplete/failed
experiment are partial diagnostic output, never a successful comparison.

## Local verification and readiness

```sh
python3 -m unittest discover -s tests -v
```

These public CLI tests use deliberately synthetic rates, array/page records
and a fake kubectl executable at the external process boundary. They exercise
preparation, scheduling, report validation, command capture, archive retrieval,
cleanup and failure retention. The fake cluster smoke is a test of software,
not actual Kubernetes placement or throughput. Native serial smoke execution
is covered by the unchanged standalone workload suite.

See [verification-10.md](../verification-10.md) for checks actually executed,
and [milestone-1.md](../milestone-1.md) for readiness and limits. Linux/OpenMP
container/cluster runs and VM/DDR/HBM measurements remain ticket 11. No live
run, hardware verification or throughput is invented to complete this ticket.
