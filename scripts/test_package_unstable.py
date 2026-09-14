"""Offline packager tests using fixture inputs and mocked subprocesses only.

Run: python3 -B -m unittest discover -s scripts -p test_package_unstable.py -v
"""

import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import shlex
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import package_unstable as packager


def digest(data):
    return hashlib.sha256(data).hexdigest()


class PackageTests(unittest.TestCase):
    branch = "unstable"
    channel = "unstable"
    binary_name = "jetscale-unstable"
    binary_version = "0.0.0-unstable.200.3"

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        previous = Path.cwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, previous)
        self.evidence = self.root / "evidence"
        self.evidence.mkdir()
        self.output = self.root / "artifact"
        self.raw = b'{"openapi":"3.1.0","info":{"version":"2.4.0-unstable.8"},"paths":{}}'
        self.processed = b'{"openapi":"3.0.3","info":{"version":"2.4.0-unstable.8"},"paths":{}}\n'
        self.manifest = {
            "schema_version": 1, "backend_repository": "Jetscale-ai/Backend",
            "backend_workflow": ".github/workflows/pipeline.yml", "backend_event": "push",
            "backend_branch": self.branch, "backend_sha": "a" * 40,
            "backend_run_id": "100", "backend_run_attempt": "2", "spec_sha": digest(self.raw),
            "spec_version": "2.4.0-unstable.8", "artifact_name": "openapi-spec",
        }
        self.manifest_raw = json.dumps(self.manifest).encode() + b"\n"
        self.provenance = {
            **self.manifest, "cli_repository": "Jetscale-ai/cli", "cli_repository_id": 2,
            "cli_ref": f"refs/heads/{self.branch}", "cli_sha": "b" * 40, "cli_run_id": "200",
            "cli_run_attempt": "3", "cli_event": "workflow_dispatch",
            "dispatch_event": "openapi-schema-build", "artifact_id": 400,
            "artifact_digest": "sha256:" + "c" * 64, "artifact_size_in_bytes": 1024,
            "artifact_created_at": "2026-09-14T00:00:00Z", "artifact_updated_at": "2026-09-14T00:00:00Z",
            "artifact_expires_at": "2026-12-14T00:00:00Z", "backend_repository_id": 1,
            "backend_workflow_id": 5, "backend_run_url": "https://github.com/Jetscale-ai/Backend/actions/runs/100",
            "archive_sha256": "c" * 64, "manifest_sha256": digest(self.manifest_raw),
        }
        self.write_evidence()
        self.sources = {
            "cmd/jetscale/main.go": b"package main\nfunc main() {}\n",
            "internal/api/generated/client.gen.go": b"// generated client\npackage generated\n",
            "go.mod": b"module github.com/Jetscale-ai/cli\n\ngo 1.26.0\n",
            "go.sum": b"example.test/mod v1.0.0 h1:fixture\n",
            "magefile.go": b"//go:build mage\npackage main\n",
            "internal/new_test.go": b"package internal\n",
            "internal/unicode_\u00e9.go": b"package internal\n",
        }
        for name, content in {**self.sources, "openapi/spec.json": self.processed}.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        self.env = {
            "PATH": "/trusted/bin:/usr/bin", "HOME": str(self.root / "operator-home"),
            "GITHUB_REPOSITORY": "Jetscale-ai/cli", "GITHUB_REF": f"refs/heads/{self.branch}",
            "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_SHA": "b" * 40,
            "GITHUB_RUN_ID": "200", "GITHUB_RUN_ATTEMPT": "3",
            "GOPATH": str(self.root / "go"), "GOMODCACHE": str(self.root / "modules"),
            "GOCACHE": str(self.root / "go-cache"),
            "GH_TOKEN": "fixture-secret", "GITHUB_TOKEN": "fixture-secret",
            "AWS_SECRET_ACCESS_KEY": "fixture-secret", "JETSCALE_TOKEN": "fixture-secret",
            "HTTP_PROXY": "fixture-secret", "HTTPS_PROXY": "fixture-secret",
            "GOFLAGS": "-toolexec=fixture-secret", "GOENV": "fixture-secret",
            "GOWORK": "fixture-secret", "LD_PRELOAD": "fixture-secret",
            "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_VALUE_0": "fixture-secret",
        }
        for patcher in (patch.dict(os.environ, self.env, clear=True),
                        patch.object(packager.sys, "platform", "linux"),
                        patch.object(packager.platform, "machine", return_value="x86_64")):
            patcher.start()
            self.addCleanup(patcher.stop)
        self.calls = []
        self.linked = {}
        self.head = b"b" * 40 + b"\n"
        self.go_version = b"go version go1.26.1 linux/amd64\n"
        self.version_override = {}
        self.after_build = lambda: None
        self.help_output = f"Usage:\n  {self.binary_name} [command]\n".encode()
        self.fail_at = None
        self.failure = None
        mocked = patch.object(packager.subprocess, "run", side_effect=self.child)
        self.mock_run = mocked.start()
        self.addCleanup(mocked.stop)

    def write_evidence(self):
        (self.evidence / "openapi.json").write_bytes(self.raw)
        (self.evidence / "manifest.json").write_bytes(self.manifest_raw)
        (self.evidence / "provenance.json").write_text(json.dumps(self.provenance))

    def source_listing(self):
        return b"\0".join(os.fsencode(name) for name in [
            *reversed(self.sources), "magefile.go", "deleted.go", "",
        ])

    def expected_source_hash(self):
        # Independent implementation of mage's fmt.Fprintf("%d:%s%d:", ...).
        framed = bytearray()
        for name, data in sorted(self.sources.items(), key=lambda pair: pair[0].encode()):
            path = name.encode()
            framed.extend(("%d:" % len(path)).encode())
            framed.extend(path)
            framed.extend(("%d:" % len(data)).encode())
            framed.extend(data)
        return digest(framed)

    def child(self, args, **kwargs):
        self.calls.append((args, kwargs))
        if args[0] == "git":
            phase = "head" if args[3] == "rev-parse" else "sources"
        elif args[:2] == ["go", "version"]:
            phase = "go-version"
        elif args[:2] == ["go", "build"]:
            phase = "build"
        elif args[1:] == ["version", "-o", "json"]:
            phase = "version"
        elif args[1:] == ["--help"]:
            phase = "help"
        else:
            raise AssertionError(f"Unexpected subprocess: {args}")
        if phase == self.fail_at:
            raise self.failure
        if phase == "head":
            result = self.head
        elif phase == "sources":
            self.assertEqual(args, ["git", "--no-pager", "--no-optional-locks", "ls-files", "-z",
                                    "--cached", "--others", "--exclude-standard", "--", "*.go", "go.mod", "go.sum"])
            result = self.source_listing()
        elif phase == "go-version":
            result = self.go_version
        elif phase == "build":
            fields = shlex.split(args[args.index("-ldflags") + 1])
            self.assertEqual(fields[:2], ["-s", "-w"])
            for index in range(2, len(fields), 2):
                self.assertEqual(fields[index], "-X")
                name, value = fields[index + 1].split("=", 1)
                self.linked[name.removeprefix("github.com/Jetscale-ai/cli/internal/")] = value
            Path(args[args.index("-o") + 1]).write_bytes(b"fixture-native-binary")
            self.after_build()
            result = None
        elif phase == "version":
            result = json.dumps({
                "binary": self.binary_name, "version": self.linked["cmd.version"],
                "commit": self.linked["cmd.commit"], "date": self.linked["cmd.date"],
                "channel": self.linked["buildinfo.Channel"], "backend_sha": self.linked["buildinfo.BackendSHA"],
                "schema_sha256": self.linked["buildinfo.SchemaSHA256"], "api_version": self.linked["buildinfo.APIVersion"],
                "source_sha256": self.linked["buildinfo.SourceSHA256"], "source_dirty": self.linked["buildinfo.SourceDirty"],
                **self.version_override,
            }).encode()
        else:
            result = self.help_output
        return subprocess.CompletedProcess(args, 0, stdout=result)

    def invoke(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = packager.main(["--evidence", str(self.evidence), "--output", str(self.output)])
        return code, out.getvalue(), err.getvalue()

    def assert_rejected(self, message=None):
        code, out, err = self.invoke()
        self.assertEqual(code, 1)
        self.assertEqual(out, "")
        self.assertTrue(err.startswith("ERROR: "), err)
        if message:
            self.assertIn(message, err)
        self.assertNotIn("fixture-secret", err)
        self.assertNotIn("\x1b", err)
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.glob(".unstable-package-*")), [])

    def test_complete_package_and_provenance(self):
        code, out, err = self.invoke()
        self.assertEqual((code, err), (0, ""))
        result = json.loads(out)
        self.assertEqual(json.loads((self.output / "provenance.json").read_bytes()), result)
        for key, value in self.provenance.items():
            self.assertEqual(result[key], value)
        self.assertEqual(result["generator_version"], "v2.6.0")
        self.assertEqual(result["raw_schema_sha256"], digest(self.raw))
        self.assertEqual(result["processed_schema_sha256"], digest(self.processed))
        self.assertEqual(result["source_sha256"], self.expected_source_hash())
        self.assertEqual(result["generated_client_sha256"], digest(self.sources["internal/api/generated/client.gen.go"]))
        self.assertEqual(result["go_mod_sha256"], digest(self.sources["go.mod"]))
        self.assertEqual(result["go_sum_sha256"], digest(self.sources["go.sum"]))
        self.assertEqual(result["binary_sha256"], digest(b"fixture-native-binary"))
        self.assertEqual(result["go_version"], "go version go1.26.1 linux/amd64")
        self.assertEqual(result["platform"], "linux/amd64")
        self.assertEqual(result["binary_version"], self.binary_version)
        self.assertNotEqual(result["binary_version"], result["spec_version"])
        self.assertEqual(result["channel"], self.channel)
        self.assertIs(result["source_dirty"], True)
        self.assertRegex(result["build_date"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
        self.assertEqual(self.linked, {
            "cmd.version": self.binary_version, "cmd.commit": "b" * 40,
            "cmd.date": result["build_date"], "buildinfo.Channel": self.channel,
            "buildinfo.BackendSHA": "a" * 40, "buildinfo.SchemaSHA256": digest(self.processed),
            "buildinfo.APIVersion": "2.4.0-unstable.8", "buildinfo.SourceSHA256": self.expected_source_hash(),
            "buildinfo.SourceDirty": "true",
        })
        self.assertEqual({p.name for p in self.output.iterdir()}, {
            self.binary_name, "SHA256SUMS", "provenance.json", "raw-schema.json",
            "processed-schema.json", "manifest.json",
        })
        for name, raw in (("raw-schema.json", self.raw), ("processed-schema.json", self.processed),
                          ("manifest.json", self.manifest_raw)):
            self.assertEqual((self.output / name).read_bytes(), raw)
        checksum_names = []
        for line in (self.output / "SHA256SUMS").read_text().splitlines():
            checksum, name = line.split("  ")
            checksum_names.append(name)
            self.assertEqual(checksum, digest((self.output / name).read_bytes()))
        self.assertEqual(checksum_names, sorted(p.name for p in self.output.iterdir() if p.name != "SHA256SUMS"))
        self.assertEqual(stat.S_IMODE((self.output / self.binary_name).stat().st_mode), 0o755)
        self.assertEqual(list(self.root.glob(".unstable-package-*")), [])
        self.assertNotIn("fixture-secret", out)

    def test_build_flags_timeouts_and_isolated_allowlisted_environments(self):
        self.assertEqual(self.invoke()[0], 0)
        build_calls = [(args, kw) for args, kw in self.calls if args[:2] == ["go", "build"]]
        self.assertEqual(len(build_calls), 1)
        args, kw = build_calls[0]
        self.assertEqual(args[:5], ["go", "build", "-mod=readonly", "-trimpath", "-ldflags"])
        self.assertEqual(args[-1], "./cmd/jetscale")
        self.assertEqual(Path(args[-2]).name, self.binary_name)
        self.assertEqual(kw["timeout"], 120)
        self.assertEqual(kw["stdout"], subprocess.DEVNULL)
        for args, kwargs in self.calls:
            env = kwargs["env"]
            self.assertNotIn("fixture-secret", str(env))
            self.assertNotIn("GITHUB_TOKEN", env)
            self.assertNotIn("GH_TOKEN", env)
            self.assertNotIn("GITHUB_SHA", env)
            for key, expected in {"CGO_ENABLED": "0", "GOPROXY": "off", "GOSUMDB": "off",
                                  "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOFLAGS": "",
                                  "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1"}.items():
                self.assertEqual(env[key], expected)
            self.assertEqual(env["GOMODCACHE"], self.env["GOMODCACHE"])
            self.assertEqual(env["GOCACHE"], self.env["GOCACHE"])
            self.assertNotEqual(env["HOME"], self.env["HOME"])
            self.assertEqual(kwargs["stdin"], subprocess.DEVNULL)
            self.assertEqual(kwargs["stderr"], subprocess.DEVNULL)
            self.assertIs(kwargs["check"], True)
            if args[0] not in ("git", "go"):
                self.assertEqual(Path(env["HOME"]).name, "smoke-home")
            if args[:2] != ["go", "build"]:
                self.assertEqual(kwargs["timeout"], 10)

    def test_source_hash_uses_current_inputs_deduplicates_and_skips_deletions(self):
        before = packager.source_hash({})
        self.assertEqual(before, self.expected_source_hash())
        Path("cmd/jetscale/main.go").write_bytes(b"changed current source")
        self.assertNotEqual(packager.source_hash({}), before)
        Path("cmd/jetscale/main.go").write_bytes(self.sources["cmd/jetscale/main.go"])
        Path("unrelated.md").write_text("not a Go source input")
        self.assertEqual(packager.source_hash({}), before)

    def test_head_mismatch(self):
        self.head = b"d" * 40
        self.assert_rejected("Git HEAD")
        self.assertFalse(any(args[:2] == ["go", "build"] for args, _ in self.calls))

    def test_ci_context_mismatch(self):
        for key, value in {"GITHUB_SHA": "c" * 40, "GITHUB_REF": "refs/heads/feature",
                           "GITHUB_REPOSITORY": "other/cli", "GITHUB_EVENT_NAME": "pull_request",
                           "GITHUB_RUN_ID": "201", "GITHUB_RUN_ATTEMPT": "4"}.items():
            with self.subTest(key=key), patch.dict(os.environ, {key: value}):
                self.assert_rejected()

    def test_cross_branch_evidence_rejected_before_build(self):
        other = "main" if self.branch == "unstable" else "unstable"
        for change_manifest, change_provenance, change_receiver in (
            (True, False, False), (False, True, False), (True, True, False),
            (False, False, True),
        ):
            with self.subTest(manifest=change_manifest, provenance=change_provenance,
                              receiver=change_receiver):
                manifest = {**self.manifest}
                provenance = {**self.provenance}
                if change_manifest:
                    manifest["backend_branch"] = other
                if change_provenance:
                    provenance["backend_branch"] = other
                if change_receiver:
                    provenance["cli_ref"] = f"refs/heads/{other}"
                raw = json.dumps(manifest).encode() + b"\n"
                provenance["manifest_sha256"] = digest(raw)
                (self.evidence / "manifest.json").write_bytes(raw)
                (self.evidence / "provenance.json").write_text(json.dumps(provenance))
                # Even a matching receiver context cannot admit the other producer branch.
                context = {"GITHUB_REF": f"refs/heads/{other}"} if change_receiver else {}
                with patch.dict(os.environ, context):
                    self.assert_rejected()
        self.assertFalse(any(args[:2] == ["go", "build"] for args, _ in self.calls))

    def test_wrong_ci_branch_rejected(self):
        other = "main" if self.branch == "unstable" else "unstable"
        with patch.dict(os.environ, {"GITHUB_REF": f"refs/heads/{other}"}):
            self.assert_rejected("matched-branch")

    def test_missing_ci_context_rejected(self):
        for key in ("GITHUB_REPOSITORY", "GITHUB_REF", "GITHUB_EVENT_NAME", "GITHUB_SHA",
                    "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"):
            with self.subTest(key=key), patch.dict(os.environ):
                del os.environ[key]
                self.assert_rejected()

    def test_legacy_dispatch_rejected(self):
        for event, dispatch in (("repository_dispatch", "openapi-schema-build"),
                                ("workflow_dispatch", "unstable-openapi-spec-updated"),
                                ("repository_dispatch", "unstable-openapi-spec-updated")):
            with self.subTest(event=event, dispatch=dispatch):
                modified = {**self.provenance, "cli_event": event, "dispatch_event": dispatch}
                (self.evidence / "provenance.json").write_text(json.dumps(modified))
                with patch.dict(os.environ, {"GITHUB_EVENT_NAME": event}):
                    self.assert_rejected()
        self.assertFalse(any(args[:2] == ["go", "build"] for args, _ in self.calls))

    def test_arbitrary_branch_rejected(self):
        for branch in ("live", "stable", "local", "feature", "", None, [], {}):
            with self.subTest(branch=branch):
                manifest = {**self.manifest, "backend_branch": branch}
                raw = json.dumps(manifest).encode() + b"\n"
                provenance = {**self.provenance, "backend_branch": branch,
                              "manifest_sha256": digest(raw), "cli_ref": f"refs/heads/{branch}"}
                (self.evidence / "manifest.json").write_bytes(raw)
                (self.evidence / "provenance.json").write_text(json.dumps(provenance))
                with patch.dict(os.environ, {"GITHUB_REF": provenance["cli_ref"]}):
                    self.assert_rejected()
        self.assertFalse(any(args[:2] == ["go", "build"] for args, _ in self.calls))

    def test_invalid_provenance_fields(self):
        for key, value in {"cli_sha": "x", "backend_sha": "x", "cli_run_id": "01",
                           "cli_run_attempt": True, "backend_run_id": "0", "backend_run_attempt": "-1",
                           "cli_ref": "refs/heads/other", "cli_repository": "other/cli",
                           "cli_event": "pull_request", "dispatch_event": "wrong",
                           "spec_version": "1.0.0", "spec_sha": "d" * 64,
                           "manifest_sha256": "d" * 64}.items():
            with self.subTest(key=key):
                modified = {**self.provenance, key: value}
                (self.evidence / "provenance.json").write_text(json.dumps(modified))
                self.assert_rejected()
        self.write_evidence()

    def test_raw_manifest_and_processed_integrity(self):
        for name, content in (("openapi.json", self.raw + b" "),
                              ("manifest.json", self.manifest_raw + b" "),
                              ("provenance.json", b'{"cli_sha":"a","cli_sha":"b"}'),
                              ("openapi.json", b"not JSON")):
            with self.subTest(name=name, content=content):
                (self.evidence / name).write_bytes(content)
                self.assert_rejected()
                self.write_evidence()
        Path("openapi/spec.json").write_bytes(self.processed.replace(b"2.4.0", b"3.0.0"))
        self.assert_rejected("Processed API version")

    def test_linker_metadata_injection_rejected(self):
        for value in ("v1 -X malicious=value", "v1'", 'v1"', "v1\n", "v1\t", ""):
            with self.subTest(value=value):
                Path("openapi/spec.json").write_text(json.dumps({"info": {"version": value}}))
                self.assert_rejected("Invalid API version")

    def test_native_platform_and_toolchain_required(self):
        with patch.object(packager.platform, "machine", return_value="aarch64"):
            self.assert_rejected("native Linux amd64")
        with patch.object(packager.sys, "platform", "darwin"):
            self.assert_rejected("native Linux amd64")
        for version in (b"go version go1.26.1 linux/arm64", b"go version devel linux/amd64"):
            with self.subTest(version=version):
                self.go_version = version
                self.assert_rejected("Go toolchain")

    def test_output_must_be_new_with_existing_parent(self):
        for kind in ("directory", "file", "dangling-symlink"):
            with self.subTest(kind=kind):
                if kind == "directory":
                    self.output.mkdir()
                elif kind == "file":
                    self.output.write_bytes(b"keep")
                else:
                    self.output.symlink_to(self.root / "missing")
                code, out, err = self.invoke()
                self.assertEqual((code, out), (1, ""))
                self.assertTrue(err.startswith("ERROR:"))
                self.assertTrue(os.path.lexists(self.output))
                if kind == "directory":
                    self.output.rmdir()
                else:
                    self.output.unlink()
        self.output = self.root / "missing-parent" / "artifact"
        self.assert_rejected("parent directory")

    def test_concurrent_output_is_preserved(self):
        def create_output():
            self.output.mkdir()
            (self.output / "keep").write_text("human data")
        self.after_build = create_output
        code, out, err = self.invoke()
        self.assertEqual((code, out), (1, ""))
        self.assertIn("appeared", err)
        self.assertEqual((self.output / "keep").read_text(), "human data")
        self.assertEqual(list(self.root.glob(".unstable-package-*")), [])

    def test_missing_unexpected_symlink_and_special_inputs(self):
        (self.evidence / "extra").write_text("unexpected")
        self.assert_rejected("three-file")
        (self.evidence / "extra").unlink()
        for relative in ("evidence/openapi.json", "evidence/manifest.json", "evidence/provenance.json",
                         "openapi/spec.json", "internal/api/generated/client.gen.go", "go.mod", "go.sum"):
            with self.subTest(relative=relative):
                path = self.root / relative
                original = path.read_bytes()
                path.unlink()
                self.assert_rejected()
                path.symlink_to(self.root / "missing")
                self.assert_rejected()
                path.unlink()
                os.mkfifo(path)
                self.assert_rejected("bounded regular file")
                path.unlink()
                path.write_bytes(original)

    def test_symlink_directory_components_rejected(self):
        real = self.root / "actual-openapi"
        Path("openapi").rename(real)
        Path("openapi").symlink_to(real, target_is_directory=True)
        self.assert_rejected("Symlink paths")

    def test_size_limits(self):
        with patch.object(packager, "SCHEMA_LIMIT", 8):
            self.assert_rejected("bounded regular file")
        with patch.object(packager, "SOURCE_LIMIT", 8):
            self.assert_rejected("Go source inputs")
        with patch.object(packager, "CHILD_OUTPUT_LIMIT", 8):
            self.assert_rejected("Child output")

    def test_all_subprocess_failures_and_timeouts_cleanup_without_leaks(self):
        for phase in ("head", "sources", "go-version", "build", "version", "help"):
            for failure in (subprocess.CalledProcessError(23, "fixture-secret", output=b"fixture-secret", stderr=b"fixture-secret"),
                            subprocess.TimeoutExpired("fixture-secret", 120, output=b"fixture-secret"),
                            OSError("fixture-secret")):
                with self.subTest(phase=phase, failure=type(failure).__name__):
                    self.fail_at, self.failure = phase, failure
                    self.assert_rejected()

    def test_interrupt_cleans_stage(self):
        self.fail_at, self.failure = "build", KeyboardInterrupt()
        code, out, err = self.invoke()
        self.assertEqual((code, out, err), (130, "", "ERROR: Packaging interrupted\n"))
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.glob(".unstable-package-*")), [])

    def test_each_embedded_metadata_mismatch_rejected(self):
        for field in ("binary", "version", "commit", "date", "channel", "backend_sha", "schema_sha256",
                      "api_version", "source_sha256", "source_dirty"):
            with self.subTest(field=field):
                self.version_override = {field: "unexpected"}
                self.assert_rejected("Binary version metadata")

    def test_wrong_channel_binary_and_version_rejected(self):
        other_channel, other_binary, other_version = (
            ("live", "jetscale", "0.0.0-main.200.3") if self.branch == "unstable"
            else ("unstable", "jetscale-unstable", "0.0.0-unstable.200.3")
        )
        for field, value in (("channel", other_channel), ("binary", other_binary),
                             ("version", other_version), ("channel", "local"),
                             ("version", "1.2.3")):
            with self.subTest(field=field, value=value):
                self.version_override = {field: value}
                self.assert_rejected("Binary version metadata")

    def test_no_user_selected_channel_option(self):
        for channel in ("live", "unstable", "local"):
            with self.subTest(channel=channel), contextlib.redirect_stderr(io.StringIO()), \
                    self.assertRaises(SystemExit) as exit_info:
                packager.main(["--evidence", str(self.evidence), "--output", str(self.output),
                               "--channel", channel])
            self.assertEqual(exit_info.exception.code, 2)
        self.mock_run.assert_not_called()
        self.assertFalse(self.output.exists())

    def test_invalid_help_rejected(self):
        self.help_output = b"unexpected output"
        self.assert_rejected("Binary help")

    def test_workspace_changes_during_build_rejected(self):
        for relative in ("cmd/jetscale/main.go", "go.mod", "go.sum", "openapi/spec.json",
                         "internal/api/generated/client.gen.go", "evidence/provenance.json"):
            with self.subTest(relative=relative):
                path = self.root / relative
                original = path.read_bytes()
                changed = (json.dumps({**self.provenance, "archive_sha256": "d" * 64}).encode()
                           if relative == "evidence/provenance.json" else original + b" ")
                self.after_build = lambda: path.write_bytes(changed)
                self.assert_rejected("inputs changed")
                path.write_bytes(original)

    def test_no_partial_output_after_write_or_rename_failure(self):
        with patch.object(packager.os, "rename", side_effect=OSError("fixture-secret")):
            self.assert_rejected()
        original = Path.write_bytes
        def fail_provenance(path, data):
            if path.name == "provenance.json" and path.parent.name == "stage":
                raise OSError("fixture-secret")
            return original(path, data)
        with patch.object(Path, "write_bytes", fail_provenance):
            self.assert_rejected()

    def test_help_is_offline_and_documents_executable_bit(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out), self.assertRaises(SystemExit) as exit_info:
            packager.main(["--help"])
        self.assertEqual(exit_info.exception.code, 0)
        self.assertIn("chmod +x jetscale-unstable", out.getvalue())
        self.assertIn("chmod +x jetscale)", out.getvalue())
        self.assertIn("not stable releases", out.getvalue())
        self.assertIn("sha256sum -c SHA256SUMS", out.getvalue())
        self.assertNotIn("\x1b", out.getvalue())
        self.mock_run.assert_not_called()


class MainPackageTests(PackageTests):
    """Run the same integrity, isolation, and failure checks for the main lane."""

    branch = "main"
    channel = "live"
    binary_name = "jetscale"
    binary_version = "0.0.0-main.200.3"


if __name__ == "__main__":
    unittest.main()
