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


"""Strict Linux CPU/node list parsing shared by checker and archived-evidence reporting."""

import re


def parse_list(value: str) -> set[int]:
    result: set[int] = set()
    if not value.strip():
        return result
    for part in value.strip().split(","):
        if not re.fullmatch(r"[0-9]+(?:-[0-9]+)?", part):
            raise ValueError(f"invalid CPU or node list: {value!r}")
        bounds = [int(number) for number in part.split("-")]
        low, high = bounds[0], bounds[-1]
        if low > high or high > 1048575:
            raise ValueError(f"invalid or oversized CPU/node range: {value!r}")
        result.update(range(low, high + 1))
    return result



def status_lists(status: str) -> tuple[set[int], set[int]]:
    fields = {}
    for line in status.splitlines():
        if ":" in line:
            key, value = line.split(":", 1)
            if key in fields:
                raise ValueError(f"duplicate proc status field: {key}")
            fields[key] = value.strip()
    return parse_list(fields["Cpus_allowed_list"]), parse_list(fields["Mems_allowed_list"])
