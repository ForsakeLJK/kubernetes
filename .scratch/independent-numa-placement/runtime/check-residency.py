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


"""Check sampled locations of the probe's touched mappings, independently of masks."""

import argparse
import hashlib
import json
from pathlib import Path
import sys
from typing import Any

from residency import assess


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("observation", type=Path)
    parser.add_argument("--memory-node", type=int, required=True)
    parser.add_argument("--hugepage-size", action="append", default=[])
    parser.add_argument("--output", type=Path, help="new evidence JSON")
    args = parser.parse_args()
    if not 0 <= args.memory_node <= 63:
        parser.error("--memory-node must be in 0..63")
    result: dict[str, Any] = {"status": "NOT RUN", "memory_node": args.memory_node,
                              "source": str(args.observation), "scope": "sampled touched pages at observation time"}
    try:
        raw = args.observation.read_bytes()
        observation = json.loads(raw)
        status, reasons = assess(observation, args.memory_node, args.hugepage_size)
        result.update(status=status, reasons=reasons, container=observation["container"],
                      source_sha256=hashlib.sha256(raw).hexdigest(), observation=observation)
    except (OSError, ValueError, KeyError, TypeError) as error:
        result["reasons"] = [f"incomplete residency evidence {args.observation}: {error}"]
    if args.output:
        try:
            with args.output.open("x") as file:
                json.dump(result, file, indent=2, allow_nan=False)
                file.write("\n")
        except OSError as error:
            print(f"cannot persist residency evidence {args.output}: {error}", file=sys.stderr)
            return 2
    print(json.dumps(result))
    return {"PASS": 0, "FAIL": 1, "NOT RUN": 2}[result["status"]]


if __name__ == "__main__":
    raise SystemExit(main())
