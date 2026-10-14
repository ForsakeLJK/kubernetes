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


"""Report operator-adjudicated cases, refusing passes with missing or failed evidence."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import sys
from typing import Any

from fixtures import CASES, FORMS, PLACEMENT, save, validated
from residency import assess

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "explicit-numa-placement/runtime"))
from masklib import parse_list, status_lists

ROLES = {"init", "sidecar", "app-a", "app-b"}


def artifact(run: Path, name: str) -> Path:
    if not isinstance(name, str) or not name or Path(name).is_absolute():
        raise ValueError("artifact must have a nonempty run-relative path")
    path = (run / name).resolve()
    if not path.is_relative_to(run):
        raise ValueError(f"artifact escapes run directory: {name}")
    return path


def record_file(run: Path, name: str, hashes: dict[str, str], allow_empty: bool = False) -> Path:
    path = artifact(run, name)
    data = path.read_bytes()
    if not data and not allow_empty:
        raise ValueError(f"empty evidence: {name}")
    hashes[str(path.relative_to(run))] = hashlib.sha256(data).hexdigest()
    return path


def command_evidence(run: Path, name: str, hashes: dict[str, str]) -> None:
    command = json.loads(record_file(run, name + "/command.json", hashes).read_text())
    if not isinstance(command, dict):
        raise ValueError(f"command capture must contain a JSON object: {name}")
    if (command.get("status") != "completed" or type(command.get("returncode")) is not int
            or not isinstance(command.get("command"), list) or not command["command"]
            or not all(isinstance(part, str) for part in command["command"])):
        raise ValueError(f"incomplete command capture: {name}")
    start, end = datetime.fromisoformat(command["started"]), datetime.fromisoformat(command["finished"])
    if start.tzinfo is None or end.tzinfo is None or end < start:
        raise ValueError(f"invalid command timestamps: {name}")
    for stream in ["stdout.txt", "stderr.txt"]:
        record_file(run, name + "/" + stream, hashes, allow_empty=True)


def environment_issues(run: Path, env: dict[str, Any], config: dict[str, Any], hashes: dict[str, str]) -> list[str]:
    if not isinstance(env, dict):
        raise ValueError("environment.json must contain an object")
    issues = []
    for field in ["operator", "date", "commit", "apiserver_commit", "kubelet_commit",
                  "apiserver_version", "kubelet_version", "kernel", "runtime", "image", "boundary"]:
        if not isinstance(env.get(field), str) or not env[field].strip():
            issues.append(f"missing environment.{field}")
    commit = env.get("commit", "")
    if (not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit)
            or env.get("apiserver_commit") != commit or env.get("kubelet_commit") != commit):
        issues.append("missing matching full fork/API-server/kubelet commit identity")
    if env.get("image") != config["image"]:
        issues.append("environment image differs from configured image digest")
    try:
        datetime.fromisoformat(env.get("date", ""))
    except (ValueError, TypeError):
        issues.append("missing valid ISO environment date")
    for slot in ["topology", "config", "versions"]:
        try:
            paths = env["artifacts"][slot]
            if not isinstance(paths, list) or not paths:
                raise ValueError(f"missing environment artifact {slot}")
            for name in paths:
                record_file(run, name, hashes)
        except (OSError, ValueError, KeyError, TypeError) as error:
            issues.append(f"missing environment {slot}: {error}")
    return issues


def masks(record: dict[str, Any]) -> tuple[int, int]:
    cpu, memory = record["cpu_node"], record["memory_node"]
    cpus, mems = record["cpus"], record["memory_nodes"]
    if (type(cpu) is not int or type(memory) is not int or not 0 <= cpu <= 63 or not 0 <= memory <= 63
            or not isinstance(cpus, list) or not cpus or any(type(item) is not int or item < 0 for item in cpus)
            or mems != [memory] or not isinstance(record.get("raw_status"), str) or not record["raw_status"]
            or not isinstance(record.get("node_cpus"), str) or not record.get("pod")):
        raise ValueError("incomplete or inconsistent effective mask record")
    observed_cpus, observed_mems = status_lists(record["raw_status"])
    if observed_cpus != set(cpus) or observed_mems != set(mems):
        raise ValueError("raw status and parsed mask record disagree")
    if not set(cpus) <= parse_list(record["node_cpus"]):
        raise ValueError("recorded effective CPUs escape selected CPU node")
    return cpu, memory


def case_report(run: Path, case: dict[str, Any], config: dict[str, Any], env_issues: list[str],
                hashes: dict[str, str]) -> dict[str, Any]:
    requested = case["result"]
    result = {**case, "issues": []}
    issues: list[str] = result["issues"]
    if requested == "NOT RUN":
        return result
    issues.extend(env_issues)
    if case["id"] in ["hugepages", "hugepage-exhaustion"] and not config["hugepages"]:
        issues.append("missing provisioned hugepage sizes; this prerequisite cannot pass")
    failed = requested == "FAIL"
    mask_records, residency_records = [], []
    for slot in CASES[case["id"]]:
        paths = case.get("evidence", {}).get(slot, [])
        if not isinstance(paths, list) or not paths:
            issues.append(f"missing {slot} evidence")
            continue
        for name in paths:
            try:
                if slot == "commands":
                    command_evidence(run, name, hashes)
                    continue
                path = record_file(run, name, hashes)
                if slot in ["masks", "residency"]:
                    evidence = json.loads(path.read_text())
                    if not isinstance(evidence, dict):
                        raise ValueError("checker record must contain a JSON object")
                    if evidence.get("status") == "FAIL":
                        failed = True
                        issues.append(f"failed {slot} checker: {name}")
                    elif evidence.get("status") != "PASS":
                        issues.append(f"incomplete {slot} checker: {name}")
                    elif slot == "masks":
                        masks(evidence)
                        mask_records.append(evidence)
                    else:
                        node = evidence["memory_node"]
                        if type(node) is not int or not 0 <= node <= 63:
                            raise ValueError("invalid residency target")
                        sizes = [page["size"] for page in config["hugepages"]] if case["id"] == "hugepages" else []
                        status, reasons = assess(evidence["observation"], node, sizes)
                        if status == "FAIL":
                            failed = True
                        if status != "PASS":
                            issues.append(f"{name}: {'; '.join(reasons)}")
                        else:
                            if evidence.get("container") != evidence["observation"]["container"]:
                                raise ValueError("checker and observation container identities disagree")
                            residency_records.append(evidence)
            except (OSError, ValueError, KeyError, TypeError) as error:
                issues.append(f"missing/invalid {slot} evidence {name}: {error}")
    if "masks" in CASES[case["id"]]:
        for label, records in [("masks", mask_records), ("residency", residency_records)]:
            roles = {record.get("container") for record in records}
            if not ROLES <= roles:
                issues.append(f"missing {label} for containers {sorted(ROLES - roles)}")
        for record in residency_records:
            identity = (record.get("namespace"), record.get("pod"), record.get("container"))
            if not all(isinstance(value, str) and value for value in identity):
                issues.append("missing namespace/Pod/container identity in residency record")
                continue
            matching = [mask for mask in mask_records if
                        (mask.get("namespace"), mask.get("pod"), mask.get("container")) == identity]
            if not matching:
                issues.append(f"missing mask for residency identity {identity}")
            elif any(mask["memory_node"] != record["memory_node"] for mask in matching):
                failed = True
                issues.append(f"residency target differs from observed memory mask for {identity}")
    if case["id"] in [*FORMS, "memory-only-node"] and mask_records:
        pairs = {(record["cpu_node"], record["memory_node"]) for record in mask_records}
        if len(pairs) != 1:
            failed = True
            issues.append("covered containers do not share one CPU/memory pair")
        else:
            cpu, memory = next(iter(pairs))
            form = case["id"]
            if ((form in ["explicit", "hugepages", "cpu-only", "memory-only-node"] and cpu != config["cpu_node"])
                    or (form in ["explicit", "hugepages", "memory-only", "memory-only-node"] and memory != config["memory_node"])
                    or (form in ["equal", "legacy"] and (cpu, memory) != (config["equal_node"], config["equal_node"]))
                    or (form == "ordinary" and cpu != memory)):
                failed = True
                issues.append("observed pair conflicts with fixture placement requirement")
            if any(record["memory_node"] != memory for record in residency_records):
                failed = True
                issues.append("residency target differs from observed memory mask")
    result["result"] = "FAIL" if failed else "NOT RUN" if issues else "PASS"
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True, help="new report directory")
    args = parser.parse_args()
    try:
        run = args.run.resolve()
        config = validated(json.loads((run / "config.json").read_text()))
        env = json.loads((run / "environment.json").read_text())
        records = json.loads((run / "cases.json").read_text())["cases"]
        ids = [case["id"] for case in records]
        if len(ids) != len(set(ids)) or set(ids) != set(CASES):
            raise ValueError("case checklist must contain every generated case exactly once")
        for case in records:
            if case["result"] not in ["PASS", "FAIL", "NOT RUN"] or not isinstance(case["reason"], str) or not case["reason"].strip():
                raise ValueError(f"case {case['id']} needs PASS/FAIL/NOT RUN and a nonempty reason")
            if not isinstance(case.get("evidence"), dict):
                raise ValueError(f"case {case['id']} evidence must contain an object")
        hashes: dict[str, str] = {}
        for name in ["config.json", "environment.json", "cases.json"]:
            record_file(run, name, hashes)
        issues = environment_issues(run, env, config, hashes)
        cases = [case_report(run, case, config, issues, hashes) for case in records]
        counts = {status: sum(case["result"] == status for case in cases) for status in ["PASS", "FAIL", "NOT RUN"]}
        report = {"schema_version": 1, "generated": datetime.now(timezone.utc).isoformat(),
                  "scope": "operator adjudication with evidence-completeness checks; no inferred runtime results",
                  "environment": env, "environment_issues": issues, "counts": counts, "cases": cases, "sha256": hashes}
        args.output.mkdir(parents=True, exist_ok=False)
        save(args.output / "report.json", report)
        lines = ["# NUMA runtime evidence", "", report["scope"], "",
                 "| Case | Result | Reason / evidence issues |", "| --- | --- | --- |"]
        for case in cases:
            reason = case["reason"] + ("; " + "; ".join(case["issues"]) if case["issues"] else "")
            reason = reason.replace("|", "\\|").replace("\n", " ")
            lines.append(f"| {case['id']} | {case['result']} | {reason} |")
        lines += ["", f"PASS {counts['PASS']}; FAIL {counts['FAIL']}; NOT RUN {counts['NOT RUN']}.", "",
                  "See report.json for environment, artifact paths, hashes, and checker scope."]
        (args.output / "report.md").write_text("\n".join(lines) + "\n")
        print(json.dumps(counts))
        return 1 if counts["FAIL"] else 2 if counts["NOT RUN"] else 0
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.exit(2, f"cannot report {args.run} into {args.output}: {error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
