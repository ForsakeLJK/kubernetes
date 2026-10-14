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


"""Capture an explicit command argv and raw output in a new evidence directory."""

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import subprocess
import sys
from typing import Any


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=600)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or not 0 < args.timeout <= 86400:
        parser.error("supply -- COMMAND [ARG ...] and a timeout in (0, 86400]")
    record: dict[str, Any] = {"command": command, "status": "running",
                              "started": datetime.now(timezone.utc).isoformat()}
    try:
        args.output.mkdir(parents=True, exist_ok=False)
        path = args.output / "command.json"
        path.write_text(json.dumps(record, indent=2) + "\n")
        with (args.output / "stdout.txt").open("wb") as stdout, (args.output / "stderr.txt").open("wb") as stderr:
            try:
                result = subprocess.run(command, stdout=stdout, stderr=stderr, timeout=args.timeout)
                record.update(status="completed", returncode=result.returncode)
            except (OSError, subprocess.TimeoutExpired) as error:
                record.update(status="timeout" if isinstance(error, subprocess.TimeoutExpired) else "launch_failed",
                              error=str(error), returncode=None)
        record["finished"] = datetime.now(timezone.utc).isoformat()
        path.write_text(json.dumps(record, indent=2) + "\n")
        print(json.dumps({"output": str(args.output), **record}))
        return 0 if record.get("returncode") == 0 else 1
    except OSError as error:
        print(f"cannot persist command evidence in {args.output}: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
