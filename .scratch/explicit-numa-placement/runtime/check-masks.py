#!/usr/bin/env python3
"""Check a running container's effective CPU and memory NUMA masks."""

import argparse
import subprocess
import sys


def parse_list(value):
    result = set()
    for part in value.strip().split(","):
        if not part:
            continue
        bounds = part.split("-")
        if len(bounds) == 1:
            result.add(int(bounds[0]))
        elif len(bounds) == 2 and int(bounds[0]) <= int(bounds[1]):
            result.update(range(int(bounds[0]), int(bounds[1]) + 1))
        else:
            raise ValueError(f"invalid CPU or node list: {value!r}")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--namespace", default="default")
    parser.add_argument("--pod", required=True)
    parser.add_argument("--container", required=True)
    parser.add_argument("--node", required=True, type=int)
    parser.add_argument("--node-cpus", required=True,
                        help="contents of /sys/devices/system/node/nodeN/cpulist on the Linux node")
    args = parser.parse_args()

    command = ["kubectl", "-n", args.namespace, "exec", args.pod,
               "-c", args.container, "--", "cat", "/proc/self/status"]
    status = subprocess.run(command, capture_output=True, text=True, check=True).stdout
    fields = {}
    for line in status.splitlines():
        if ":" in line:
            key, value = line.split(":", 1)
            fields[key] = value.strip()

    cpus = parse_list(fields["Cpus_allowed_list"])
    mems = parse_list(fields["Mems_allowed_list"])
    node_cpus = parse_list(args.node_cpus)
    if not cpus or not node_cpus:
        raise ValueError("empty effective or host NUMA CPU list")
    failures = []
    if not cpus <= node_cpus:
        failures.append(f"CPUs outside node {args.node}: {sorted(cpus - node_cpus)}")
    if mems != {args.node}:
        failures.append(f"memory nodes are {sorted(mems)}, expected [{args.node}]")
    print(f"{args.namespace}/{args.pod}/{args.container}: "
          f"CPUs={fields['Cpus_allowed_list']} memory nodes={fields['Mems_allowed_list']}")
    if failures:
        print("FAIL: " + "; ".join(failures), file=sys.stderr)
        return 1
    print(f"PASS: effective masks select NUMA node {args.node}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except subprocess.CalledProcessError as error:
        print(f"FAIL: kubectl exec failed: {error.stderr.strip()}", file=sys.stderr)
        sys.exit(2)
    except (KeyError, ValueError, OSError) as error:
        print(f"FAIL: could not verify effective masks: {error}", file=sys.stderr)
        sys.exit(2)
