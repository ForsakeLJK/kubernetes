#!/usr/bin/env python3
# Copyright 2026 The Kubernetes Authors.
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


"""Parse official STREAM 5.10 stdout without converting its raw MB/s units."""

import argparse
import json
import math
from pathlib import Path
import re
from typing import Any

KERNELS = ("Copy", "Scale", "Add", "Triad")


def parse_output(stdout: str, returncode: int = 0) -> dict[str, Any]:
    kernels = {}
    errors = []
    for line in stdout.splitlines():
        match = re.fullmatch(r"(Copy|Scale|Add|Triad):\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s*", line)
        if match:
            try:
                rate, avg, minimum, maximum = map(float, match.groups()[1:])
                if not all(math.isfinite(value) for value in (rate, avg, minimum, maximum)):
                    raise ValueError("nonfinite value")
                if rate <= 0 or not 0 <= minimum <= avg <= maximum:
                    raise ValueError("nonpositive rate or inconsistent times")
                if match[1] in kernels:
                    raise ValueError("duplicate kernel row")
                kernels[match[1]] = {"best_rate": rate, "unit": "MB/s", "avg_seconds": avg,
                                     "min_seconds": minimum, "max_seconds": maximum}
            except ValueError as error:
                errors.append(f"{match[1]}: {error}")
    if not re.search(r"^Function\s+Best Rate MB/s\s+Avg time\s+Min time\s+Max time\s*$", stdout, re.M):
        errors.append("missing upstream MB/s table header")
    if set(kernels) != set(KERNELS):
        errors.append("missing or malformed kernel rows")
    validated = re.findall(r"^Solution Validates: avg error less than [0-9.eE+-]+ on all three arrays\s*$", stdout, re.M)
    validation = "pass" if len(validated) == 1 else "missing"
    if "Failed Validation" in stdout:
        validation = "fail"
    status = "valid"
    if validation == "fail":
        status = "invalid_numerics"
    elif validation != "pass" or errors:
        status = "invalid_output"
    if returncode != 0:
        status = "process_failed"
    ticks = re.search(r"\(= (\d+) clock ticks\)", stdout)
    return {"schema_version": 1, "status": status, "validation": validation,
            "returncode": returncode, "kernels": kernels, "errors": errors,
            "calibration_ticks": int(ticks[1]) if ticks else None}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stdout", type=Path)
    parser.add_argument("--returncode", type=int, default=0)
    args = parser.parse_args()
    result = parse_output(args.stdout.read_text(errors="replace"), args.returncode)
    print(json.dumps(result, indent=2, allow_nan=False))
    return 0 if result["status"] == "valid" else 1


if __name__ == "__main__":
    raise SystemExit(main())
