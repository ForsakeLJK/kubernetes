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


"""Validate query-only observations of specifically identified touched mappings."""

import re
from typing import Any


def assess(observation: dict[str, Any], memory_node: int, hugepage_sizes: list[str]) -> tuple[str, list[str]]:
    if not isinstance(observation, dict):
        raise ValueError("observation must be a JSON object")
    missing, failures = [], []
    if observation.get("allocation_status") == "FAIL":
        return "FAIL", ["probe mapping allocation failed; inspect process stderr and resource capacity"]
    if (observation.get("phase") != "touched" or observation.get("allocation_status") != "PASS"
            or observation.get("method") != "move_pages query only"
            or type(observation.get("pid")) is not int or observation["pid"] <= 0
            or type(observation.get("observed_unix")) is not int or observation["observed_unix"] <= 0
            or not observation.get("container")):
        raise ValueError("missing touched phase, allocation, process, container, time or query identity")
    page = observation["page_size"]
    if type(page) is not int or page <= 0 or page & (page - 1):
        raise ValueError("page_size must be a positive power of two")
    mappings = observation["mappings"]
    if not isinstance(mappings, list) or not mappings:
        raise ValueError("no identified touched mappings")
    names = set()
    for mapping in mappings:
        if not isinstance(mapping, dict):
            raise ValueError("mapping must be a JSON object")
        name = mapping["name"]
        if not isinstance(name, str) or not name or name in names:
            raise ValueError("missing or duplicate touched mapping name")
        names.add(name)
        if mapping.get("allocation_errno"):
            failures.append(f"{name}: allocation failed ({mapping['allocation_errno']})")
            continue
        start, length = int(mapping["start"], 16), mapping["bytes"]
        total, sampled = mapping["total_pages"], mapping["sampled_pages"]
        if (any(type(value) is not int for value in [length, total, sampled])
                or start <= 0 or start % page or length <= 0
                or total != (length - 1) // page + 1 or not 1 <= sampled <= total):
            raise ValueError(f"{name}: inconsistent mapping address/size/sample counts")
        if name == "anonymous":
            if mapping.get("backing") != "private-anonymous":
                raise ValueError("anonymous mapping lacks private anonymous backing identity")
        else:
            if not re.fullmatch(r"[1-9][0-9]*(?:Ki|Mi|Gi)", name) or mapping.get("backing") != "hugetlbfs":
                raise ValueError(f"{name}: mapping lacks a real hugetlbfs/page-size identity")
            huge_size = int(name[:-2]) * {"Ki": 1024, "Mi": 1024**2, "Gi": 1024**3}[name[-2:]]
            if length % huge_size or start % huge_size:
                raise ValueError(f"{name}: mapping is not aligned/sized for the requested hugepage size")
        nodes = mapping["page_nodes"]
        if type(mapping["errno"]) is not int or mapping["errno"] < 0:
            raise ValueError(f"{name}: invalid query errno")
        if mapping["errno"]:
            missing.append(f"{name}: page query unavailable (errno {mapping['errno']})")
        elif not isinstance(nodes, list) or len(nodes) != sampled or any(type(node) is not int for node in nodes):
            missing.append(f"{name}: incomplete page-query results")
        else:
            if any(node < 0 for node in nodes):
                missing.append(f"{name}: per-page query errors {sorted(set(node for node in nodes if node < 0))}")
            wrong = sorted(set(node for node in nodes if node >= 0 and node != memory_node))
            if wrong:
                failures.append(f"{name}: sampled pages on {wrong}, expected node {memory_node}")
    absent = {"anonymous", *hugepage_sizes} - names
    if absent:
        missing.append(f"missing requested mappings: {sorted(absent)}")
    return ("FAIL" if failures else "NOT RUN" if missing else "PASS"), failures + missing
