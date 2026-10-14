#!/usr/bin/env python3
# Copyright The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


"""Check separate effective CPU and memory NUMA masks; --node retains legacy usage."""

import argparse
import json
from pathlib import Path
import subprocess
import sys
from typing import Any

from masklib import parse_list, status_lists


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--namespace", default="default")
    parser.add_argument("--pod", required=True)
    parser.add_argument("--container", required=True)
    parser.add_argument("--node", type=int, help="legacy common CPU/memory target")
    parser.add_argument("--cpu-node", type=int)
    parser.add_argument("--memory-node", type=int)
    parser.add_argument("--node-cpus", required=True, help="actual CPU target's sysfs cpulist")
    parser.add_argument("--status-file", type=Path, help="archived workload /proc/1/status; skips exec")
    parser.add_argument("--output", type=Path, help="new machine-readable evidence file")
    args = parser.parse_args()
    if args.node is not None:
        if args.cpu_node is not None or args.memory_node is not None:
            parser.error("use either --node or both --cpu-node and --memory-node")
        args.cpu_node = args.memory_node = args.node
    if args.cpu_node is None or args.memory_node is None:
        parser.error("supply --node or both --cpu-node and --memory-node")
    if not 0 <= args.cpu_node <= 63 or not 0 <= args.memory_node <= 63:
        parser.error("NUMA targets must be in 0..63")
    command = ["kubectl", "-n", args.namespace, "exec", args.pod,
               "-c", args.container, "--", "cat", "/proc/1/status"]
    record: dict[str, Any] = {"status": "NOT RUN", "namespace": args.namespace,
                              "pod": args.pod, "container": args.container,
                              "cpu_node": args.cpu_node, "memory_node": args.memory_node,
                              "node_cpus": args.node_cpus,
                              "source": str(args.status_file) if args.status_file else command}
    code = 2
    try:
        if args.output and args.output.exists():
            raise ValueError(f"output already exists: {args.output}; use a new evidence filename")
        status = args.status_file.read_text() if args.status_file else subprocess.run(
            command, capture_output=True, text=True, check=True, timeout=60).stdout
        record["raw_status"] = status
        cpus, mems = status_lists(status)
        node_cpus = parse_list(args.node_cpus)
        if not cpus or not node_cpus or not mems or max(mems) > 63:
            raise ValueError("empty effective/target mask or memory node outside 0..63")
        failures = []
        if not cpus <= node_cpus:
            failures.append(f"CPUs outside node {args.cpu_node}: {sorted(cpus - node_cpus)}")
        if mems != {args.memory_node}:
            failures.append(f"memory nodes are {sorted(mems)}, expected [{args.memory_node}]")
        record.update(status="FAIL" if failures else "PASS", cpus=sorted(cpus),
                      memory_nodes=sorted(mems), failures=failures)
        code = 1 if failures else 0
    except subprocess.CalledProcessError as error:
        record["reason"] = f"kubectl exec failed ({error.returncode}): {error.stderr.strip()}"
    except (KeyError, ValueError, OSError, subprocess.TimeoutExpired) as error:
        record["reason"] = f"could not verify effective masks: {error}"
    if args.output:
        try:
            with args.output.open("x") as file:
                json.dump(record, file, indent=2)
                file.write("\n")
        except OSError as error:
            print(f"cannot persist mask evidence {args.output}: {error}", file=sys.stderr)
            return 2
    print(json.dumps(record))
    return code


if __name__ == "__main__":
    raise SystemExit(main())
