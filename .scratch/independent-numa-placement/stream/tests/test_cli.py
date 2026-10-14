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

"""Behavioral checks at the approved parser and runner CLI boundaries."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tests" / "fixtures"


class ParserCLI(unittest.TestCase):
    def parse(self, name, returncode=0):
        result = subprocess.run(
            [sys.executable, str(ROOT / "parse.py"), str(FIXTURES / name),
             "--returncode", str(returncode)], capture_output=True, text=True,
        )
        self.assertTrue(result.stdout, result.stderr)
        return result.returncode, json.loads(result.stdout)

    def test_preserves_all_four_kernels_units_and_upstream_validation(self):
        code, result = self.parse("valid.stdout")
        self.assertEqual(code, 0)
        self.assertEqual(result["status"], "valid")
        self.assertEqual(result["validation"], "pass")
        self.assertEqual(result["kernels"], {
            "Copy": {"best_rate": 12000.0, "unit": "MB/s", "avg_seconds": 0.0014,
                     "min_seconds": 0.001333, "max_seconds": 0.0015},
            "Scale": {"best_rate": 10000.0, "unit": "MB/s", "avg_seconds": 0.0017,
                      "min_seconds": 0.0016, "max_seconds": 0.0018},
            "Add": {"best_rate": 15000.0, "unit": "MB/s", "avg_seconds": 0.0017,
                    "min_seconds": 0.0016, "max_seconds": 0.0018},
            "Triad": {"best_rate": 16000.0, "unit": "MB/s", "avg_seconds": 0.0016,
                      "min_seconds": 0.0015, "max_seconds": 0.0017},
        })

    def test_rejects_wrong_units_duplicate_rows_bad_times_and_missing_validation(self):
        for fixture in ['wrong-units.stdout', 'duplicate.stdout', 'bad-times.stdout',
                        'missing-validation.stdout']:
            with self.subTest(fixture=fixture):
                code, result = self.parse(fixture)
                self.assertEqual(code, 1)
                self.assertEqual(result["status"], "invalid_output")

    def test_rejects_nonfinite_bandwidth(self):
        code, result = self.parse("nonfinite.stdout")
        self.assertEqual(code, 1)
        self.assertEqual(result["status"], "invalid_output")

    def test_rejects_upstream_numerical_failure_even_with_exit_zero(self):
        code, result = self.parse("invalid-numerics.stdout")
        self.assertEqual(code, 1)
        self.assertEqual(result["status"], "invalid_numerics")
        self.assertEqual(result["validation"], "fail")

    def test_rejects_truncated_output(self):
        code, result = self.parse("truncated.stdout")
        self.assertEqual(code, 1)
        self.assertEqual(result["status"], "invalid_output")

    def test_never_accepts_successful_text_from_a_failed_process(self):
        code, result = self.parse("valid.stdout", -9)
        self.assertEqual(code, 1)
        self.assertEqual(result["status"], "process_failed")
        self.assertEqual(result["returncode"], -9)


class RunnerCLI(unittest.TestCase):
    def test_arrays_are_live_in_an_untimed_observation_window(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "run"
            process = subprocess.Popen(
                [sys.executable, str(ROOT / "run.py"), "--output", str(output),
                 "--serial", "--cc", "cc", "--array-size", "1000000",
                 "--observe-seconds", "2"], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True,
            )
            try:
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline and not (output / "arrays.json").exists():
                    self.assertIsNone(process.poll(), "runner exited before the observation window")
                    time.sleep(0.01)
                identity = json.loads((output / "arrays.json").read_text())
                os.kill(identity["pid"], 0)
                self.assertIsNone(process.poll())
                self.assertNotIn("Function    Best Rate", (output / "stdout.txt").read_text())
                stdout, stderr = process.communicate(timeout=20)
                self.assertEqual(process.returncode, 0, stderr + stdout)
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.communicate(timeout=10)

    def test_rejects_invalid_size_threads_and_iterations_without_losing_metadata(self):
        for flags in [['--array-size', '0'], ['--threads', '0'], ['--iterations', '1'],
                      ['--threads', '2'], ['--timeout', '0']]:
            with self.subTest(flags=flags), tempfile.TemporaryDirectory() as temporary:
                output = Path(temporary) / "run"
                run = subprocess.run(
                    [sys.executable, str(ROOT / "run.py"), "--output", str(output),
                     "--serial", "--observe-seconds", "0"] + flags,
                    capture_output=True, text=True,
                )
                self.assertEqual(run.returncode, 1, run.stderr)
                meta = json.loads((output / "run.json").read_text())
                self.assertEqual(meta["status"], "setup_failed")
                self.assertTrue(meta["error"])

    def test_numeric_failure_retains_raw_output_even_when_upstream_exits_zero(self):
        with tempfile.TemporaryDirectory() as temporary:
            temporary = Path(temporary)
            compiler = temporary / "compiler"
            fixture = (FIXTURES / "invalid-numerics.stdout").read_text()
            binary = "#!" + sys.executable + "\nimport sys\nprint(" + repr(fixture) + ")\nprint('fixture stderr', file=sys.stderr)\n"
            compiler.write_text("#!" + sys.executable + "\nimport pathlib, sys\n"
                                "if '--version' in sys.argv: print('fixture compiler'); sys.exit(0)\n"
                                "p=pathlib.Path(sys.argv[sys.argv.index('-o')+1])\n"
                                "p.write_text(" + repr(binary) + ")\np.chmod(0o755)\n")
            compiler.chmod(0o755)
            output = temporary / "run"
            run = subprocess.run(
                [sys.executable, str(ROOT / "run.py"), "--output", str(output),
                 "--serial", "--cc", str(compiler), "--observe-seconds", "0"],
                capture_output=True, text=True,
            )
            self.assertEqual(run.returncode, 1, run.stderr)
            meta = json.loads((output / "run.json").read_text())
            self.assertEqual(meta["status"], "invalid_numerics")
            self.assertEqual(meta["returncode"], 0)
            self.assertIn("Failed Validation", (output / "stdout.txt").read_text())
            self.assertIn("fixture stderr", (output / "stderr.txt").read_text())

    def test_compiler_failure_retains_failure_status_and_logs(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "run"
            run = subprocess.run(
                [sys.executable, str(ROOT / "run.py"), "--output", str(output),
                 "--serial", "--cc", "/usr/bin/false", "--observe-seconds", "0"],
                capture_output=True, text=True,
            )
            self.assertEqual(run.returncode, 1, run.stderr)
            meta = json.loads((output / "run.json").read_text())
            self.assertEqual(meta["status"], "setup_failed")
            self.assertIn("returned non-zero exit status", meta["error"])
            self.assertTrue((output / "stdout.txt").is_file())
            self.assertTrue((output / "stderr.txt").is_file())

    def test_serial_smoke_retains_build_identity_logs_arrays_and_validation(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "run"
            run = subprocess.run(
                [sys.executable, str(ROOT / "run.py"), "--output", str(output),
                 "--serial", "--cc", "cc", "--array-size", "1000000",
                 "--threads", "1", "--observe-seconds", "0"],
                capture_output=True, text=True,
            )
            self.assertEqual(run.returncode, 0, run.stderr + run.stdout)
            meta = json.loads((output / "run.json").read_text())
            self.assertEqual(meta["status"], "valid")
            self.assertEqual(meta["classification"], "smoke")
            self.assertEqual(meta["result"]["validation"], "pass")
            self.assertEqual(meta["observed_threads"], 1)
            self.assertEqual(meta["array_identity_status"], "verified")
            self.assertEqual(len(meta["build"]["binary_sha256"]), 64)
            self.assertIn("-O3", meta["build"]["command"])
            self.assertNotIn("-fopenmp", meta["build"]["command"])
            self.assertIn("Solution Validates:", (output / "stdout.txt").read_text())
            self.assertTrue((output / "stderr.txt").is_file())
            identity = json.loads((output / "arrays.json").read_text())
            self.assertEqual(identity["phase"], "initialized_before_calibration")
            self.assertGreater(identity["pid"], 0)
            self.assertEqual([a["name"] for a in identity["arrays"]], ["a", "b", "c"])
            self.assertEqual([a["bytes"] for a in identity["arrays"]], [8000000] * 3)
            for array in identity["arrays"]:
                self.assertEqual(int(array["end"], 16) - int(array["start"], 16), 8000000)
            self.assertIn(json.loads((output / "residency.json").read_text())["status"],
                          ["sampled", "unavailable"])


class ContainerCLI(unittest.TestCase):
    def test_runtime_oom_survives_missing_inner_result(self):
        with tempfile.TemporaryDirectory() as temporary:
            temporary = Path(temporary)
            engine = temporary / "engine"
            engine.write_text("#!" + sys.executable + "\n" + r'''import json, pathlib, sys
args = sys.argv[1:]
if args[:2] == ["image", "inspect"]:
    print(json.dumps([{"Id": "sha256:fixture-image", "RepoDigests": []}]))
elif args[0] == "run":
    pathlib.Path(args[args.index("--cidfile")+1]).write_text("fixture-container")
    print("fixture workload killed", file=sys.stderr)
    sys.exit(137)
elif args[0] == "inspect":
    print(json.dumps([{"Id": "fixture-container", "Image": "sha256:fixture-image",
                       "State": {"ExitCode": 137, "OOMKilled": True}}]))
else:
    sys.exit(2)
''')
            engine.chmod(0o755)
            output = temporary / "evidence"
            result = subprocess.run(
                [sys.executable, str(ROOT / "container.py"), "--engine", str(engine),
                 "--image", "fixture-image", "--output", str(output), "--", "--threads", "1"],
                capture_output=True, text=True,
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            meta = json.loads((output / "launch.json").read_text())
            self.assertEqual(meta["status"], "oom")
            self.assertEqual(meta["returncode"], 137)
            self.assertTrue(meta["oom_killed"])
            self.assertEqual(meta["image_id"], "sha256:fixture-image")
            self.assertIn("fixture workload killed", (output / "runtime.stderr.txt").read_text())
            self.assertTrue((output / "container-inspect.json").is_file())


if __name__ == "__main__":
    unittest.main()
