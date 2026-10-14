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


"""Synthetic public CLI evidence tests; these are not Linux placement measurements."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
MASKS = ROOT.parent.parent / "explicit-numa-placement/runtime/check-masks.py"


class EvidenceCLI(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        self.status = self.directory / "status.txt"
        self.status.write_text("Name:\tprobe\nCpus_allowed_list:\t8-9,24\nMems_allowed_list:\t3\n")

    def tearDown(self):
        self.temporary.cleanup()

    def cli(self, script, *args):
        return subprocess.run([sys.executable, str(script), *map(str, args)], capture_output=True, text=True)

    def test_split_sparse_masks_can_be_checked_without_memory_node_cpus(self):
        output = self.directory / "mask.json"
        result = self.cli(MASKS, "--pod", "test", "--container", "init", "--cpu-node", 7,
                          "--memory-node", 3, "--node-cpus", "8-11,24-27", "--status-file", self.status,
                          "--output", output)
        self.assertEqual(result.returncode, 0, result.stderr)
        evidence = json.loads(output.read_text())
        self.assertEqual((evidence["status"], evidence["cpu_node"], evidence["memory_node"]), ("PASS", 7, 3))
        self.assertEqual(evidence["cpus"], [8, 9, 24])
        self.assertEqual(evidence["memory_nodes"], [3])

    def test_legacy_and_invalid_masks_have_honest_exit_status(self):
        for cpu_mask, memory_mask, expected in [("8-9,24", "7", 0), ("8,30", "7", 1),
                                                  ("8", "3,7", 1), ("8,", "7", 2),
                                                  ("", "7", 2), ("8", "", 2)]:
            with self.subTest(cpu_mask=cpu_mask, memory_mask=memory_mask):
                self.status.write_text(f"Cpus_allowed_list: {cpu_mask}\nMems_allowed_list: {memory_mask}\n")
                result = self.cli(MASKS, "--pod", "test", "--container", "app-a", "--node", 7,
                                  "--node-cpus", "8-11,24-27", "--status-file", self.status)
                self.assertEqual(result.returncode, expected, result.stderr)
        self.status.write_text("Cpus_allowed_list: 8\n")
        result = self.cli(MASKS, "--pod", "test", "--container", "sidecar", "--node", 7,
                          "--node-cpus", "8-11", "--status-file", self.status)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(json.loads(result.stdout)["status"], "NOT RUN")

    def test_touched_sparse_residency_is_distinct_from_allowed_masks(self):
        observation = self.directory / "observation.json"
        raw = {"pid": 1, "container": "init", "phase": "touched", "observed_unix": 1,
               "method": "move_pages query only", "page_size": 4096, "allocation_status": "PASS",
               "mappings": [{"name": "anonymous", "backing": "private-anonymous", "start": "0x10000", "bytes": 12288,
                             "total_pages": 3, "sampled_pages": 3, "errno": 0, "page_nodes": [3, 3, 3]}]}
        observation.write_text(json.dumps(raw))
        output = self.directory / "residency.json"
        result = self.cli(ROOT / "check-residency.py", observation, "--memory-node", 3, "--output", output)
        self.assertEqual(result.returncode, 0, result.stderr)
        evidence = json.loads(output.read_text())
        self.assertEqual(evidence["status"], "PASS")
        self.assertEqual(evidence["container"], "init")
        raw["mappings"][0]["page_nodes"] = [3, 7, 3]
        observation.write_text(json.dumps(raw))
        result = self.cli(ROOT / "check-residency.py", observation, "--memory-node", 3)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(json.loads(result.stdout)["status"], "FAIL")

    def test_missing_page_queries_and_hugepage_mappings_are_not_passes(self):
        path = self.directory / "observation.json"
        original = {"pid": 1, "container": "app-a", "phase": "touched", "observed_unix": 1,
                    "method": "move_pages query only", "page_size": 4096, "allocation_status": "PASS",
                    "mappings": [{"name": "anonymous", "backing": "private-anonymous", "start": "0x10000", "bytes": 12288,
                                  "total_pages": 3, "sampled_pages": 3, "errno": 0, "page_nodes": [3, 3, 3]}]}
        for change in [{"errno": 1, "page_nodes": []}, {"page_nodes": [3]},
                       {"page_nodes": [3, -14, 3]}, {"sampled_pages": 0}]:
            with self.subTest(change=change):
                raw = json.loads(json.dumps(original))
                raw["mappings"][0].update(change)
                path.write_text(json.dumps(raw))
                result = self.cli(ROOT / "check-residency.py", path, "--memory-node", 3)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertEqual(json.loads(result.stdout)["status"], "NOT RUN")
        path.write_text(json.dumps(original))
        result = self.cli(ROOT / "check-residency.py", path, "--memory-node", 3, "--hugepage-size", "2Mi")
        self.assertEqual(result.returncode, 2)

    def test_report_preserves_all_cases_and_downgrades_incomplete_pass(self):
        config = json.loads((ROOT / "config.example.json").read_text())
        config.update(node="worker", image="test/probe@sha256:" + "a" * 64)
        config_path = self.directory / "config.json"
        config_path.write_text(json.dumps(config))
        run = self.directory / "run"
        generated = self.cli(ROOT / "fixtures.py", "--config", config_path, "--output", run)
        self.assertEqual(generated.returncode, 0, generated.stderr)
        path = run / "cases.json"
        records = json.loads(path.read_text())
        records["cases"][0].update(result="PASS", reason="synthetic claimed pass with no evidence")
        path.write_text(json.dumps(records))
        output = run / "report-01"
        result = self.cli(ROOT / "report.py", "--run", run, "--output", output)
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((output / "report.json").read_text())
        self.assertGreaterEqual(len(report["cases"]), 30)
        self.assertEqual(report["cases"][0]["result"], "NOT RUN")
        self.assertIn("missing", " ".join(report["cases"][0]["issues"]))
        self.assertIn("NOT RUN", (output / "report.md").read_text())

    def prepared_report_run(self):
        config = json.loads((ROOT / "config.example.json").read_text())
        config.update(node="worker", image="test/probe@sha256:" + "a" * 64)
        config_path = self.directory / "config.json"
        config_path.write_text(json.dumps(config))
        run = self.directory / "run"
        generated = self.cli(ROOT / "fixtures.py", "--config", config_path, "--output", run)
        self.assertEqual(generated.returncode, 0, generated.stderr)
        (run / "raw.txt").write_text("Synthetic topology/config/selection artifact; not a host measurement\n")
        environment = json.loads((run / "environment.json").read_text())
        environment.update(operator="synthetic-test", date="2026-10-14", commit="b" * 40,
                           apiserver_commit="b" * 40, kubelet_commit="b" * 40,
                           apiserver_version="test", kubelet_version="test", kernel="test", runtime="test",
                           boundary="synthetic inputs only",
                           artifacts={"topology": ["raw.txt"], "config": ["raw.txt"], "versions": ["raw.txt"]})
        (run / "environment.json").write_text(json.dumps(environment))
        capture = self.cli(ROOT / "capture.py", "--output", run / "command-01", "--",
                           sys.executable, "-c", "print('synthetic external command')")
        self.assertEqual(capture.returncode, 0, capture.stderr)
        records = json.loads((run / "cases.json").read_text())
        case = records["cases"][0]
        case.update(result="PASS", reason="synthetic operator adjudication")
        case["evidence"].update(manifest=["manifests/explicit.json"], commands=["command-01"],
                                pod=["raw.txt"], selection=["raw.txt"])
        for role in ["init", "sidecar", "app-a", "app-b"]:
            mask = {"status": "PASS", "pod": "numa-explicit", "container": role, "cpu_node": 0,
                    "memory_node": 3, "node_cpus": "8-11,24", "cpus": [8], "memory_nodes": [3],
                    "raw_status": "Cpus_allowed_list: 8\nMems_allowed_list: 3\n"}
            (run / (role + "-mask.json")).write_text(json.dumps(mask))
            case["evidence"]["masks"].append(role + "-mask.json")
            observation = {"pid": 1, "container": role, "phase": "touched", "observed_unix": 1,
                           "method": "move_pages query only", "page_size": 4096, "allocation_status": "PASS",
                           "mappings": [{"name": "anonymous", "backing": "private-anonymous", "start": "0x10000", "bytes": 4096,
                                         "total_pages": 1, "sampled_pages": 1, "errno": 0, "page_nodes": [3]}]}
            evidence = {"status": "PASS", "container": role, "memory_node": 3, "observation": observation}
            (run / (role + "-residency.json")).write_text(json.dumps(evidence))
            case["evidence"]["residency"].append(role + "-residency.json")
        (run / "cases.json").write_text(json.dumps(records))
        return run

    def test_report_accepts_complete_case_and_detects_failed_or_missing_roles(self):
        run = self.prepared_report_run()
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-valid")
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((run / "report-valid/report.json").read_text())
        self.assertEqual(report["cases"][0]["result"], "PASS")
        self.assertEqual(report["counts"]["PASS"], 1)
        self.assertIn("app-a-mask.json", report["sha256"])
        path = run / "app-a-mask.json"
        mask = json.loads(path.read_text())
        mask["status"] = "FAIL"
        path.write_text(json.dumps(mask))
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-fail")
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((run / "report-fail/report.json").read_text())
        self.assertEqual(report["cases"][0]["result"], "FAIL")
        mask["status"] = "PASS"
        path.write_text(json.dumps(mask))
        (run / "init-mask.json").unlink()
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-missing")
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((run / "report-missing/report.json").read_text())
        self.assertEqual(report["cases"][0]["result"], "NOT RUN")

    def test_report_rejects_missing_cases_stale_commands_and_escaped_paths(self):
        run = self.prepared_report_run()
        command_path = run / "command-01/command.json"
        command = json.loads(command_path.read_text())
        command["status"] = "running"
        command_path.write_text(json.dumps(command))
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-stale")
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((run / "report-stale/report.json").read_text())
        self.assertEqual(report["cases"][0]["result"], "NOT RUN")
        cases_path = run / "cases.json"
        cases = json.loads(cases_path.read_text())
        cases["cases"][0]["evidence"]["manifest"] = ["../config.json"]
        cases_path.write_text(json.dumps(cases))
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-escape")
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((run / "report-escape/report.json").read_text())
        self.assertIn("escapes run", " ".join(report["cases"][0]["issues"]))
        cases["cases"].pop()
        cases_path.write_text(json.dumps(cases))
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-hidden")
        self.assertEqual(result.returncode, 2)
        self.assertIn("every generated case", result.stderr)

    def test_report_rechecks_raw_mask_output_and_handles_malformed_observations(self):
        run = self.prepared_report_run()
        path = run / "app-a-mask.json"
        mask = json.loads(path.read_text())
        mask["raw_status"] = "Cpus_allowed_list: 8\nMems_allowed_list: 3,7\n"
        path.write_text(json.dumps(mask))
        result = self.cli(ROOT / "report.py", "--run", run, "--output", run / "report-inconsistent")
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((run / "report-inconsistent/report.json").read_text())
        self.assertEqual(report["cases"][0]["result"], "NOT RUN")
        observation = self.directory / "bad-observation.json"
        observation.write_text("[]")
        result = self.cli(ROOT / "check-residency.py", observation, "--memory-node", 3)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertNotIn("Traceback", result.stderr)

    def test_hugepage_evidence_requires_real_backing_and_complete_requested_sizes(self):
        observation = self.directory / "pages.json"
        original = {"pid": 1, "container": "app-a", "phase": "touched", "observed_unix": 1,
                    "method": "move_pages query only", "page_size": 4096, "allocation_status": "PASS",
                    "mappings": [
                        {"name": "anonymous", "backing": "private-anonymous", "start": "0x10000", "bytes": 4096,
                         "total_pages": 1, "sampled_pages": 1, "errno": 0, "page_nodes": [3]},
                        {"name": "2Mi", "backing": "regular-file", "start": "0x200000", "bytes": 2097152,
                         "total_pages": 512, "sampled_pages": 2, "errno": 0, "page_nodes": [3, 3]}]}
        observation.write_text(json.dumps(original))
        result = self.cli(ROOT / "check-residency.py", observation, "--memory-node", 3, "--hugepage-size", "2Mi")
        self.assertEqual(result.returncode, 2, result.stderr)
        original["mappings"][1]["backing"] = "hugetlbfs"
        observation.write_text(json.dumps(original))
        result = self.cli(ROOT / "check-residency.py", observation, "--memory-node", 3, "--hugepage-size", "2Mi")
        self.assertEqual(result.returncode, 0, result.stderr)
        result = self.cli(ROOT / "check-residency.py", observation, "--memory-node", 3,
                          "--hugepage-size", "2Mi", "--hugepage-size", "1Gi")
        self.assertEqual(result.returncode, 2)


if __name__ == "__main__":
    unittest.main()
