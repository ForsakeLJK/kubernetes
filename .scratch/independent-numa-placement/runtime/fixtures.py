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


"""Generate machine-specific manifests and an initially NOT RUN acceptance checklist."""

import argparse
import json
from pathlib import Path
import re
from typing import Any

FORMS = ["explicit", "cpu-only", "memory-only", "equal", "legacy", "ordinary", "hugepages"]
PLACEMENT = ["manifest", "commands", "pod", "selection", "masks", "residency"]
CASES = {
    **{form: PLACEMENT for form in FORMS},
    "hugepages": PLACEMENT + ["capacity"],
    "memory-only-node": PLACEMENT + ["topology"],
    "container-restart": PLACEMENT + ["before", "after"],
    "kubelet-restart": PLACEMENT + ["before", "after", "journal"],
    "post-recovery-start": PLACEMENT + ["before", "after", "journal"],
    "cpu-exhaustion": ["manifest", "commands", "refusal", "capacity", "before", "after"],
    "memory-exhaustion": ["manifest", "commands", "refusal", "capacity", "before", "after"],
    "hugepage-exhaustion": ["manifest", "commands", "refusal", "capacity", "before", "after"],
    "automatic-aggregate-only": ["manifest", "commands", "refusal", "capacity"],
    "nearest-exhausted": PLACEMENT + ["capacity", "topology"],
    "missing-id": ["manifest", "commands", "refusal", "topology"],
    "cpuless-cpu-target": ["manifest", "commands", "refusal", "topology"],
    "invalid-api": ["manifest", "commands", "refusal"],
    "ineligible": ["manifest", "commands", "refusal"],
    "unsupported-config": ["manifest", "commands", "refusal", "config", "journal"],
    "immutable": ["manifest", "commands", "refusal"],
    "resize": ["manifest", "commands", "refusal", "config"],
    "ephemeral": ["manifest", "commands", "refusal", "config"],
    "unrelated-update": PLACEMENT,
    "device-compatible": PLACEMENT + ["device"],
    "device-conflict": ["manifest", "commands", "refusal", "device", "before", "after"],
    "legacy-old-policy": PLACEMENT + ["config", "before", "after"],
    "ordinary-old-policy": PLACEMENT + ["config", "before", "after"],
    "failure-cleanup": ["manifest", "commands", "refusal", "before", "after", "retry", "unrelated"],
    "normal-cleanup": ["manifest", "commands", "before", "after", "retry", "unrelated"],
    "partial-rollback": ["manifest", "commands", "refusal", "before", "allocated", "after", "unrelated"],
    "distance-data-refusal": ["manifest", "commands", "refusal", "topology"],
    "recovery-conflict": ["manifest", "commands", "refusal", "before", "after"],
}


def save(path: Path, value: Any) -> None:
    with path.open("x") as file:
        json.dump(value, file, indent=2, allow_nan=False)
        file.write("\n")


def validated(config: dict[str, Any]) -> dict[str, Any]:
    for field in ["cpu_node", "memory_node", "equal_node"]:
        if type(config[field]) is not int or not 0 <= config[field] <= 63:
            raise ValueError(f"{field} must be an actual NUMA ID in 0..63")
    for field in ["node", "namespace"]:
        if not re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", config[field]):
            raise ValueError(f"{field} must be an actual Kubernetes name")
    if not re.fullmatch(r"[^\s]+@sha256:[0-9a-f]{64}", config["image"]):
        raise ValueError("image must be resolved as NAME@sha256:DIGEST")
    if type(config["cpu"]) is not int or config["cpu"] < 1:
        raise ValueError("cpu must be a positive whole CPU count per container")
    if not re.fullmatch(r"[1-9][0-9]*(?:Ki|Mi|Gi)", config["memory"]):
        raise ValueError("memory must be a positive Ki/Mi/Gi quantity")
    units = {"Ki": 1024, "Mi": 1024**2, "Gi": 1024**3}
    memory = int(config["memory"][:-2]) * units[config["memory"][-2:]]
    if type(config["touch_bytes"]) is not int or not 1 <= config["touch_bytes"] < memory:
        raise ValueError("touch_bytes must be positive and leave process overhead below memory limit")
    if type(config["hold_seconds"]) is not int or not 1 <= config["hold_seconds"] <= 86400:
        raise ValueError("hold_seconds must be 1..86400")
    seen = set()
    for pages in config["hugepages"]:
        size = pages["size"]
        if not re.fullmatch(r"[1-9][0-9]*(?:Ki|Mi|Gi)", size) or size in seen:
            raise ValueError("hugepage sizes must be distinct positive Ki/Mi/Gi quantities")
        page_bytes = int(size[:-2]) * units[size[-2:]]
        if type(pages["bytes"]) is not int or pages["bytes"] <= 0 or pages["bytes"] % page_bytes:
            raise ValueError(f"hugepage bytes must be a positive multiple of {size}")
        seen.add(size)
    return config


def manifest(config: dict[str, Any], form: str, name: str, single: bool) -> dict[str, Any]:
    resources = {"cpu": str(config["cpu"]), "memory": config["memory"]}
    pages = config["hugepages"] if form == "hugepages" else []
    if form == "hugepages" and not pages:
        raise ValueError("hugepages fixture requires provisioned sizes; keep its case NOT RUN")
    for page in pages:
        resources["hugepages-" + page["size"]] = str(page["bytes"])
    def container(name: str) -> dict[str, Any]:
        command = ["/opt/numa/probe", "--container", name, "--bytes", str(config["touch_bytes"]),
                   "--hold-seconds", str(config["hold_seconds"])]
        if name == "init":
            command += ["--release", "/tmp/numa-release"]
        mounts = []
        for index, page in enumerate(pages):
            mount = "/hugepages-" + page["size"]
            command += ["--hugepage", page["size"], mount + "/" + name, str(page["bytes"])]
            mounts.append({"name": f"pages-{index}", "mountPath": mount})
        item = {"name": name, "image": config["image"], "command": command,
                "resources": {"requests": resources.copy(), "limits": resources.copy()}}
        if name == "sidecar":
            item["restartPolicy"] = "Always"
        if mounts:
            item["volumeMounts"] = mounts
        return item
    spec: dict[str, Any] = {"nodeName": config["node"], "restartPolicy": "Always",
                            "containers": [container("app-a")]}
    if not single:
        spec["containers"].append(container("app-b"))
        spec["initContainers"] = [container("sidecar"), container("init")]
    if form in ["explicit", "hugepages", "cpu-only"]:
        spec["cpuNUMA"] = config["cpu_node"]
    if form in ["explicit", "hugepages", "memory-only"]:
        spec["memNUMA"] = config["memory_node"]
    if form == "equal":
        spec.update(cpuNUMA=config["equal_node"], memNUMA=config["equal_node"])
    if form == "legacy":
        spec["numaNode"] = config["equal_node"]
    if pages:
        spec["volumes"] = [{"name": f"pages-{i}", "emptyDir": {"medium": "HugePages-" + p["size"]}}
                           for i, p in enumerate(pages)]
    return {"apiVersion": "v1", "kind": "Pod",
            "metadata": {"name": name, "namespace": config["namespace"]}, "spec": spec}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True, help="new run directory, or manifest file with --form")
    parser.add_argument("--form", choices=FORMS)
    parser.add_argument("--name", default="numa-fill")
    parser.add_argument("--single", action="store_true", help="single app for capacity fillers; requires --form")
    args = parser.parse_args()
    try:
        config = validated(json.loads(args.config.read_text()))
        if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", args.name):
            raise ValueError("name must be a valid Pod name")
        if args.single and not args.form:
            raise ValueError("--single requires --form")
        if args.form:
            save(args.output, manifest(config, args.form, args.name, args.single))
        else:
            args.output.mkdir(parents=True, exist_ok=False)
            (args.output / "manifests").mkdir()
            save(args.output / "config.json", config)
            for form in FORMS:
                if form == "hugepages" and not config["hugepages"]:
                    continue
                save(args.output / "manifests" / (form + ".json"), manifest(config, form, "numa-" + form, False))
            save(args.output / "cases.json", {"schema_version": 1, "cases": [
                {"id": case, "result": "NOT RUN", "reason": "Not executed", "evidence": {slot: [] for slot in slots}}
                for case, slots in CASES.items()]})
            save(args.output / "environment.json", {
                "operator": "", "date": "", "commit": "", "apiserver_commit": "", "kubelet_commit": "",
                "apiserver_version": "", "kubelet_version": "", "kernel": "", "runtime": "",
                "image": config["image"], "boundary": "", "artifacts": {"topology": [], "config": [], "versions": []}})
        return 0
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.exit(2, f"cannot generate {args.output}: {error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
