"""Offline contract tests for the actual embedded workflow scripts; no Docker/API calls.

Run: python3 -B -m unittest discover -s scripts -p test_unstable_workflow.py -v
"""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest
from unittest.mock import patch

from unstable_build import VerificationError

WORKFLOW = Path(__file__).resolve().parents[1] / ".github/workflows/build-cli.yml"
TEXT = WORKFLOW.read_text()
SANDBOX = "Generate, test, and package in isolated offline sandbox"
VALIDATE = "Validate sandbox package on trusted host"


def step(name):
    return TEXT.split(f"      - name: {name}\n", 1)[1].split("\n      - name: ", 1)[0]


def script(name):
    return textwrap.dedent(step(name).split("        run: |\n", 1)[1])


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.checkout = self.root / "trusted"
        self.checkout.mkdir()
        self.temp = self.root / "runner-temp"
        self.temp.mkdir()
        self.evidence = self.temp / "verified-schema"
        self.evidence.mkdir()
        (self.temp / "unstable-go/pkg/mod").mkdir(parents=True)
        (self.checkout / ".git").mkdir()
        (self.checkout / ".git/HEAD").write_text("trusted head\n")
        (self.checkout / "go.mod").write_bytes(b"trusted module\n")
        (self.checkout / "go.sum").write_bytes(b"trusted sums\n")
        previous = Path.cwd()
        os.chdir(self.checkout)
        self.addCleanup(os.chdir, previous)
        original_path = sys.path[:]
        self.addCleanup(lambda: sys.path.__setitem__(slice(None), original_path))
        self.env = {
            "RUNNER_TEMP": str(self.temp), "GITHUB_WORKSPACE": str(self.checkout),
            "GITHUB_REPOSITORY": "Jetscale-ai/cli", "GITHUB_REF": "refs/heads/unstable",
            "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_SHA": "b" * 40,
            "GITHUB_RUN_ID": "200", "GITHUB_RUN_ATTEMPT": "3",
            "GH_TOKEN": "must-not-enter-sandbox", "ACTIONS_RUNTIME_TOKEN": "must-not-enter-sandbox",
            "GITHUB_EVENT_PATH": "/host/event.json", "GITHUB_ENV": "/host/env",
            "GITHUB_OUTPUT": "/host/output", "HTTP_PROXY": "must-not-enter-sandbox",
        }
        environment = patch.dict(os.environ, self.env, clear=True)
        environment.start()
        self.addCleanup(environment.stop)

    def execute(self, name):
        exec(compile(script(name), str(WORKFLOW) + ":" + name, "exec"), {})

    def make_package(self):
        self.package = self.temp / "unstable-output/package"
        self.package.mkdir(parents=True)
        self.original = {
            "cli_run_id": "200", "cli_run_attempt": "3", "cli_sha": "b" * 40,
                        "backend_branch": "unstable", "cli_ref": "refs/heads/unstable",
                        "backend_repository": "Jetscale-ai/Backend", "backend_workflow": ".github/workflows/pipeline.yml",
            "backend_sha": "a" * 40, "backend_run_id": "100", "backend_run_attempt": "2",
            "backend_run_url": "https://github.com/Jetscale-ai/Backend/actions/runs/100",
            "spec_version": "2.4.0", "artifact_id": 400,
        }
        self.data = {
            # Deliberately not executable: host validation must never try to run the binary.
            "jetscale-unstable": b"fixture binary\n",
            "raw-schema.json": b'{"info":{"version":"2.4.0"}}',
            "processed-schema.json": b'{"info":{"version":"2.4.0"}}\n',
            "manifest.json": b'{"fixture":"manifest"}',
        }
        self.provenance = {
            **self.original,
            "raw_schema_sha256": digest(self.data["raw-schema.json"]),
            "processed_schema_sha256": digest(self.data["processed-schema.json"]),
            "binary_sha256": digest(self.data["jetscale-unstable"]),
            "go_mod_sha256": digest((self.checkout / "go.mod").read_bytes()),
            "go_sum_sha256": digest((self.checkout / "go.sum").read_bytes()),
            "generator_version": "v2.6.0", "go_version": "go version go1.25.8 linux/amd64",
            "platform": "linux/amd64", "channel": "unstable", "source_dirty": True,
            "binary_version": "0.0.0-unstable.200.3",
            "source_sha256": "d" * 64, "generated_client_sha256": "e" * 64,
            "build_date": "2026-09-14T00:00:00Z",
        }
        (self.evidence / "openapi.json").write_bytes(self.data["raw-schema.json"])
        (self.evidence / "manifest.json").write_bytes(self.data["manifest.json"])
        (self.evidence / "provenance.json").write_text(json.dumps(self.original))
        self.write_package()

    def write_package(self):
        self.data["provenance.json"] = json.dumps(self.provenance).encode()
        for name, raw in self.data.items():
            (self.package / name).write_bytes(raw)
        self.refresh_checksums()

    def refresh_checksums(self):
        sums = "".join(
            f"{digest((self.package / name).read_bytes())}  {name}\n"
            for name in sorted(self.data)
        )
        (self.package / "SHA256SUMS").write_text(sums)

    def assert_rejected(self):
        with self.assertRaises((VerificationError, OSError)):
            self.execute(VALIDATE)
        self.assertFalse((self.temp / "unstable-package").exists())

    def test_sandbox_uses_only_copy_and_narrow_mounts(self):
        with patch("subprocess.run") as run:
            self.execute(SANDBOX)
        command = run.call_args_list[0].args[0]
        mounts = [command[i + 1] for i, value in enumerate(command) if value == "--mount"]
        self.assertCountEqual(mounts, [
            f"type=bind,src={self.temp / 'unstable-work'},dst=/work",
            f"type=bind,src={self.temp / 'unstable-output'},dst=/output",
            f"type=bind,src={self.evidence},dst=/evidence,readonly",
            f"type=bind,src={self.temp / 'unstable-go/pkg/mod'},dst=/modules,readonly",
        ])
        work = self.temp / "unstable-work"
        self.assertEqual((work / ".git/HEAD").read_text(), "trusted head\n")
        self.assertNotEqual((work / "go.mod").stat().st_ino, (self.checkout / "go.mod").stat().st_ino)
        (work / "go.mod").write_text("sandbox edit")
        self.assertEqual((self.checkout / "go.mod").read_bytes(), b"trusted module\n")
        self.assertTrue((self.temp / "unstable-output").is_dir())
        self.assertFalse((self.temp / "unstable-output/package").exists())
        self.assertFalse((self.temp / "unstable-package").exists())

    def test_sandbox_resource_environment_and_execution_contract(self):
        with patch("subprocess.run") as run:
            self.execute(SANDBOX)
        call = run.call_args_list[0]
        command = call.args[0]
        for flag in (
            "--network=none", "--cap-drop=ALL", "--security-opt=no-new-privileges",
            "--read-only", "--memory=6g", "--memory-swap=6g", "--cpus=2", "--pids-limit=512",
            "--log-driver=none", "--pull=never", "--init",
            "--tmpfs=/tmp:rw,exec,nosuid,nodev,size=2g,mode=1777",
            "--tmpfs=/cache:rw,exec,nosuid,nodev,size=2g,mode=1777",
        ):
            self.assertIn(flag, command)
        self.assertEqual(command[command.index("--user") + 1], f"{os.getuid()}:{os.getgid()}")
        variables = dict(command[i + 1].split("=", 1) for i, value in enumerate(command) if value == "--env")
        self.assertEqual({key for key in variables if key.startswith("GITHUB_")}, {
            "GITHUB_REPOSITORY", "GITHUB_REF", "GITHUB_EVENT_NAME", "GITHUB_SHA",
            "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT",
        })
        for key in ("GOPROXY", "GOSUMDB", "GOENV", "GOWORK"):
            self.assertEqual(variables[key], "off")
        self.assertEqual(variables["GOFLAGS"], "-mod=readonly")
        self.assertEqual(variables["GOTOOLCHAIN"], "local")
        self.assertEqual(variables["GOMODCACHE"], "/modules")
        self.assertNotIn("must-not-enter-sandbox", str(command))
        self.assertNotIn("--env-file", command)
        self.assertNotIn("docker.sock", str(command))
        self.assertEqual(command[-4], "sh")
        self.assertIn("GOPROXY=file:///modules/cache/download go run github.com/magefile/mage generateClient\n", command[-1])
        self.assertNotIn("export GOPROXY=", command[-1])
        self.assertNotIn("go mod tidy", command[-1])
        self.assertNotIn("github.com/magefile/mage generate\n", command[-1])
        for check in ("go test -race -count=1", "go test -tags mage -count=1", "go vet ./...",
                      "git --no-pager diff --exit-code -- go.mod go.sum",
                      "scripts/package_unstable.py --evidence /evidence --output /output/package"):
            self.assertIn(check, command[-1])
        self.assertEqual(call.kwargs["timeout"], 1200)
        for stream in ("stdin", "stdout", "stderr"):
            self.assertEqual(call.kwargs[stream], subprocess.DEVNULL)
        self.assertTrue(call.kwargs["check"])
        self.assertEqual(run.call_args_list[-1].args[0], ["docker", "rm", "--force", "unstable-cli-200-3"])

    def test_failure_and_timeout_clean_up_and_stop(self):
        for failure in (subprocess.TimeoutExpired("docker", 1200), subprocess.CalledProcessError(1, "docker")):
            with self.subTest(failure=type(failure).__name__):
                # A separate temporary directory for each attempt, like a fresh CI job.
                with tempfile.TemporaryDirectory() as temp:
                    with patch.dict(os.environ, {"RUNNER_TEMP": temp}), patch("subprocess.run", side_effect=[failure, None]) as run:
                        with self.assertRaisesRegex(SystemExit, "no artifact published"):
                            self.execute(SANDBOX)
                        self.assertEqual(run.call_count, 2)
                        self.assertEqual(run.call_args.args[0][:3], ["docker", "rm", "--force"])

    def test_host_copies_exact_verified_files_without_execution(self):
        self.make_package()
        with patch("subprocess.run") as run:
            self.execute(VALIDATE)
        run.assert_not_called()
        destination = self.temp / "unstable-package"
        self.assertEqual({p.name for p in destination.iterdir()}, {*self.data, "SHA256SUMS"})
        for path in destination.iterdir():
            original = self.package / path.name
            self.assertEqual(path.read_bytes(), original.read_bytes())
            self.assertNotEqual(path.stat().st_ino, original.stat().st_ino)
        (self.package / "jetscale-unstable").write_bytes(b"later tampering")
        self.assertEqual((destination / "jetscale-unstable").read_bytes(), b"fixture binary\n")

    def test_main_package_uses_live_binary_without_a_stable_release(self):
        self.make_package()
        os.environ["GITHUB_REF"] = "refs/heads/main"
        self.original.update(backend_branch="main", cli_ref="refs/heads/main")
        (self.evidence / "provenance.json").write_text(json.dumps(self.original))
        self.provenance.update(self.original, channel="live", binary_version="0.0.0-main.200.3")
        self.data["jetscale"] = self.data.pop("jetscale-unstable")
        (self.package / "jetscale-unstable").unlink()
        self.write_package()
        with patch("subprocess.run") as run:
            self.execute(VALIDATE)
        run.assert_not_called()
        self.assertTrue((self.temp / "unstable-package/jetscale").is_file())
        self.assertFalse((self.temp / "unstable-package/jetscale-unstable").exists())

    def test_rejects_cross_branch_package(self):
        self.make_package()
        os.environ["GITHUB_REF"] = "refs/heads/main"
        self.assert_rejected()

    def test_rejects_wrong_channel_or_version(self):
        self.make_package()
        self.provenance.update(channel="live", binary_version="1.0.0")
        self.write_package()
        self.assert_rejected()

    def test_verified_summary_selects_attempt_specific_artifact_for_each_branch(self):
        self.make_package()
        output = self.temp / "outputs"
        summary = self.temp / "summary"
        os.environ.update(GITHUB_OUTPUT=str(output), GITHUB_STEP_SUMMARY=str(summary))
        for branch, binary in (("main", "jetscale"), ("unstable", "jetscale-unstable")):
            with self.subTest(branch=branch):
                self.original.update(backend_branch=branch, cli_ref=f"refs/heads/{branch}")
                (self.evidence / "provenance.json").write_text(json.dumps(self.original))
                output.write_text("")
                summary.write_text("")
                self.execute("Summarize upstream trigger")
                self.assertEqual(output.read_text(), f"artifact_name={binary}-linux-amd64-200-3\n")
                self.assertIn(self.original["backend_run_url"], summary.read_text())
                self.assertIn("Verified upstream trigger", summary.read_text())

    def test_dispatch_contract_and_legacy_retirement(self):
        from unstable_build import INPUT_KEYS
        declarations = {line.strip().removesuffix(":") for line in TEXT.splitlines()
                        if line.startswith("      ") and not line.startswith("       ") and line.endswith(":")}
        self.assertEqual(declarations, INPUT_KEYS)
        self.assertIn("  workflow_dispatch:", TEXT)
        self.assertNotIn("repository_dispatch:", TEXT)
        self.assertIn("github.ref == 'refs/heads/main' || github.ref == 'refs/heads/unstable'", TEXT)
        self.assertIn("ref: ${{ github.sha }}", TEXT)
        legacy = WORKFLOW.with_name("sync-openapi.yml").read_text()
        self.assertNotIn("repository_dispatch:", legacy)
        self.assertNotIn("git push", legacy)
        self.assertNotIn("git commit", legacy)
        self.assertIn("permissions: {}", legacy)
        self.assertIn("exit 1", legacy)
        ci = WORKFLOW.with_name("ci.yml").read_text()
        self.assertIn("branches: [main, unstable]", ci)
        self.assertIn("if: github.event_name == 'push' && github.ref == 'refs/heads/main'", ci)
        self.assertIn("python3 -B -m unittest discover -s scripts", ci)

    def test_rejects_checksum_mismatch(self):
        self.make_package()
        (self.package / "jetscale-unstable").write_bytes(b"tampered")
        self.assert_rejected()

    def test_rejects_binary_change_even_with_recomputed_checksums(self):
        self.make_package()
        (self.package / "jetscale-unstable").write_bytes(b"tampered")
        self.refresh_checksums()
        self.assert_rejected()

    def test_rejects_producer_evidence_change(self):
        self.make_package()
        self.data["manifest.json"] = b'{"fixture":"replaced"}'
        self.write_package()
        self.assert_rejected()

    def test_rejects_raw_schema_change_even_with_recomputed_hashes(self):
        self.make_package()
        self.data["raw-schema.json"] = b'{"fixture":"replaced"}'
        self.provenance["raw_schema_sha256"] = digest(self.data["raw-schema.json"])
        self.write_package()
        self.assert_rejected()

    def test_rejects_provenance_change(self):
        self.make_package()
        self.provenance["cli_sha"] = "f" * 40
        self.write_package()
        self.assert_rejected()

    def test_rejects_sandbox_module_change(self):
        self.make_package()
        self.provenance["go_mod_sha256"] = digest(b"new dependency")
        self.write_package()
        self.assert_rejected()

    def test_rejects_wrong_toolchain(self):
        self.make_package()
        self.provenance["go_version"] = "go version go1.26.1 linux/amd64"
        self.write_package()
        self.assert_rejected()

    def test_rejects_extra_hidden_member(self):
        self.make_package()
        (self.package / ".unexpected").write_bytes(b"do not upload")
        self.assert_rejected()

    def test_rejects_symlink_file(self):
        self.make_package()
        path = self.package / "raw-schema.json"
        path.unlink()
        path.symlink_to(self.evidence / "openapi.json")
        self.assert_rejected()

    def test_rejects_symlink_package(self):
        self.make_package()
        moved = self.package.with_name("elsewhere")
        self.package.rename(moved)
        self.package.symlink_to(moved, target_is_directory=True)
        self.assert_rejected()

    def test_rejects_hardlink_file(self):
        self.make_package()
        path = self.package / "raw-schema.json"
        path.unlink()
        os.link(self.evidence / "openapi.json", path)
        self.assert_rejected()

    def test_rejects_special_file_without_blocking(self):
        self.make_package()
        path = self.package / "jetscale-unstable"
        path.unlink()
        os.mkfifo(path)
        self.assert_rejected()

    def test_rejects_oversized_metadata(self):
        self.make_package()
        (self.package / "provenance.json").write_bytes(b" " * (16384 + 1))
        self.assert_rejected()

    def test_ordering_prefetch_and_publication_contract(self):
        names = [line.removeprefix("      - name: ") for line in TEXT.splitlines() if line.startswith("      - name: ")]
        ordered = [
            "Verify producer and download exact schema",
            "Fetch pinned tools, image, and locked dependencies", SANDBOX, VALIDATE,
            "Reject producer reruns or artifact replacement before publication",
            "Publish immutable CLI build artifact",
        ]
        self.assertEqual([names.index(name) for name in ordered], sorted(names.index(name) for name in ordered))
        self.assertIn("go-version: '1.25.8'", step("Set up Go"))
        self.assertIn("cache: false", step("Set up Go"))
        prefetch = step(ordered[1])
        for text in ("go mod download", "oapi-codegen@v2.6.0 --version", "FROM golang:1.25.8-bookworm",
                     'GOPATH="${RUNNER_TEMP}/unstable-go"', "--no-install-recommends python3"):
            self.assertIn(text, prefetch)
        recheck = step(ordered[-2])
        self.assertIn('python3 scripts/unstable_build.py recheck --input "${RUNNER_TEMP}/verified-schema"', recheck)
        upload = step(ordered[-1])
        self.assertIn("actions/upload-artifact@v6", upload)
        self.assertIn("name: ${{ steps.schema.outputs.artifact_name }}", upload)
        self.assertIn("path: ${{ runner.temp }}/unstable-package/", upload)
        self.assertIn("retention-days: 14", upload)
        self.assertIn("overwrite: false", upload)
        self.assertIn("include-hidden-files: false", upload)
        self.assertNotIn("unstable-output", upload)
        self.assertIn("if: always()", step("Remove sandbox on cancellation or failure"))
        self.assertIn("timeout-minutes: 40", TEXT)
        self.assertIn("persist-credentials: false", TEXT)
        self.assertEqual(TEXT.count("secrets.JETSCALEBOT_GITHUB_TOKEN"), 2)


if __name__ == "__main__":
    unittest.main()
