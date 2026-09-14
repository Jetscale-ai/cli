"""Offline contract tests; no GitHub requests, schema generation, or credentials.

Run: python3 -B -m unittest discover -s scripts -p test_unstable_build.py -v
"""

import contextlib
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

import unstable_build as verifier


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def archive_for(raw, manifest, members=None):
    output = io.BytesIO()
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", UserWarning)
        with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as bundle:
            for name, content in (members if members is not None else [
                ("openapi.json", raw), ("openapi-manifest.json", manifest),
            ]):
                bundle.writestr(name, content)
    return output.getvalue()


class FixtureAPI:
    """Model the provider contract independently of receiver validation helpers."""

    def __init__(self, branch="unstable"):
        self.repo = {"id": 1001, "full_name": "Jetscale-ai/Backend", "fork": False}
        self.workflow = {"id": 2001, "path": ".github/workflows/pipeline.yml", "name": "Build & Release"}
        self.run = {
            "id": 3001, "run_attempt": 2, "head_sha": "a" * 40,
            "status": "completed", "conclusion": "success", "event": "push",
            "head_branch": branch, "path": ".github/workflows/pipeline.yml",
            "name": "Build & Release", "workflow_id": 2001,
            "repository": copy.deepcopy(self.repo), "head_repository": copy.deepcopy(self.repo),
        }
        self.schema = {
            "openapi": "3.1.0", "info": {"title": "Backend", "version": "2.0.0-unstable.1"},
            "paths": {}, "components": {"schemas": {"Item": {"type": "string"}}},
            "example": {"$ref": "#/components/schemas/Item"},
        }
        self.raw = json.dumps(self.schema).encode()
        self.payload = {
            "backend_sha": "a" * 40, "backend_run_id": "3001", "backend_run_attempt": "2",
            "backend_branch": branch,
            "spec_sha": sha(self.raw), "spec_version": "2.0.0-unstable.1",
            "artifact_name": "openapi-spec",
            "backend_run_url": "https://github.com/Jetscale-ai/Backend/actions/runs/3001",
        }
        self.manifest = {
            "schema_version": 1, "backend_repository": "Jetscale-ai/Backend",
            "backend_workflow": ".github/workflows/pipeline.yml", "backend_event": "push",
            "backend_branch": branch, "backend_sha": "a" * 40,
            "backend_run_id": "3001", "backend_run_attempt": "2", "spec_sha": sha(self.raw),
            "spec_version": "2.0.0-unstable.1", "artifact_name": "openapi-spec",
        }
        self.artifact = {
            "id": 4001, "name": "openapi-spec", "expired": False,
            "created_at": "2026-09-14T10:00:00Z", "updated_at": "2026-09-14T10:00:00Z",
            "expires_at": "2026-12-13T10:00:00Z",
            "workflow_run": {"id": 3001, "head_sha": "a" * 40, "head_branch": branch,
                             "repository_id": 1001, "head_repository_id": 1001},
        }
        self.refresh_archive()
        self.inventory = [self.artifact]
        self.calls = []
        self.run_reads = 0
        self.artifact_reads = 0
        self.on_run_read = None
        self.on_artifact_read = None
        self.pages = None

    def refresh_archive(self, members=None):
        self.manifest_raw = json.dumps(self.manifest, sort_keys=True).encode() + b"\n"
        self.archive = archive_for(self.raw, self.manifest_raw, members)
        self.artifact["digest"] = "sha256:" + sha(self.archive)
        self.artifact["size_in_bytes"] = len(self.archive)

    def json(self, path):
        self.calls.append(path)
        prefix = "repos/Jetscale-ai/Backend"
        if path == prefix:
            value = self.repo
        elif path == prefix + "/actions/workflows/pipeline.yml":
            value = self.workflow
        elif path == prefix + "/actions/runs/3001":
            self.run_reads += 1
            if self.on_run_read:
                self.on_run_read(self)
            value = self.run
        elif path.startswith(prefix + "/actions/runs/3001/artifacts?per_page=100&page="):
            page = int(path.rsplit("=", 1)[1])
            value = (self.pages[page - 1] if self.pages else {
                "total_count": len(self.inventory), "artifacts": self.inventory[(page - 1) * 100:page * 100],
            })
        elif path == prefix + f"/actions/artifacts/{self.artifact['id']}":
            self.artifact_reads += 1
            if self.on_artifact_read:
                self.on_artifact_read(self)
            value = self.artifact
        else:
            raise AssertionError("Unexpected API route: " + path)
        return copy.deepcopy(value)

    def get(self, path, limit):
        self.calls.append(path)
        if path != f"repos/Jetscale-ai/Backend/actions/artifacts/{self.artifact['id']}/zip":
            raise AssertionError("Unexpected download route: " + path)
        if limit != 32 * 1024 * 1024:
            raise AssertionError("Expected bounded archive download")
        return self.archive


class BuildIdentityTests(unittest.TestCase):
    def test_branch_identities(self):
        self.assertEqual(verifier.CHANNELS, {
            "main": ("live", "jetscale"), "unstable": ("unstable", "jetscale-unstable"),
        })
        for branch, channel, binary in (("main", "live", "jetscale"),
                                        ("unstable", "unstable", "jetscale-unstable")):
            with self.subTest(branch=branch):
                self.assertEqual(verifier.build_identity(branch, "6001", "2"),
                                 (channel, binary, f"0.0.0-{branch}.6001.2"))
                self.assertEqual(verifier.build_identity(branch, str(2**63 - 1), "1")[2],
                                 f"0.0.0-{branch}.{2**63 - 1}.1")

    def test_invalid_identity_inputs(self):
        for branch in (None, True, [], "", "live", "feature", "Main", "refs/heads/main", "main\n"):
            with self.subTest(branch=branch), self.assertRaises(verifier.VerificationError):
                verifier.build_identity(branch, "6001", "1")
        for value in (None, True, 1, [], "", "0", "01", "-1", "1.0", "1\n", "1/2", str(2**63)):
            for run_id, attempt in ((value, "1"), ("6001", value)):
                with self.subTest(run_id=run_id, attempt=attempt), self.assertRaises(verifier.VerificationError):
                    verifier.build_identity("main", run_id, attempt)


class ReceiverTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.configure_branch("unstable")

    def configure_branch(self, branch):
        self.api = FixtureAPI(branch)
        self.event = {
            "ref": branch,
            "repository": {"id": 5001, "full_name": "Jetscale-ai/cli", "fork": False, "default_branch": "main"},
            "inputs": {
                "upstream_repo": "Jetscale-ai/Backend",
                "upstream_workflow": ".github/workflows/pipeline.yml",
                "upstream_run_id": self.api.payload["backend_run_id"],
                "upstream_run_url": self.api.payload["backend_run_url"],
                "upstream_sha": self.api.payload["backend_sha"],
                "backend_run_attempt": "2", "backend_branch": branch,
                "spec_sha": self.api.payload["spec_sha"],
                "spec_version": self.api.payload["spec_version"], "artifact_name": "openapi-spec",
            },
        }
        self.env = {
            "GITHUB_EVENT_PATH": str(self.root / "event.json"),
            "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REPOSITORY": "Jetscale-ai/cli",
            "GITHUB_REF": f"refs/heads/{branch}", "GITHUB_SHA": "b" * 40,
            "GITHUB_RUN_ID": "6001", "GITHUB_RUN_ATTEMPT": "1",
        }
        self.write_event()
        self.payload, self.consumer = verifier.context(self.env)

    def write_event(self):
        Path(self.env["GITHUB_EVENT_PATH"]).write_text(json.dumps(self.event))

    def verify(self):
        return verifier.verify(self.api, self.payload, self.consumer)

    def test_fetch_and_recheck_happy_path(self):
        for branch in ("main", "unstable"):
            with self.subTest(branch=branch):
                self.configure_branch(branch)
                self.assertEqual(self.payload, self.api.payload)
                self.assertEqual(set(self.payload), verifier.PAYLOAD_KEYS)
                self.assertEqual(self.consumer["cli_ref"], f"refs/heads/{branch}")
                self.assertEqual(self.consumer["cli_event"], "workflow_dispatch")
                self.assertEqual(self.consumer["dispatch_event"], "openapi-schema-build")
                self.assert_fetch_and_recheck()

    def assert_fetch_and_recheck(self):
        output = self.root / self.payload["backend_branch"]
        provenance = verifier.fetch(output, self.api, self.payload, self.consumer)
        self.assertEqual({p.name for p in output.iterdir()}, verifier.FILES)
        self.assertEqual((output / "openapi.json").read_bytes(), self.api.raw)
        self.assertEqual((output / "manifest.json").read_bytes(), self.api.manifest_raw)
        self.assertEqual(json.loads((output / "provenance.json").read_bytes()), provenance)
        self.assertEqual(provenance, {
            **self.api.manifest, **self.consumer,
            "backend_repository_id": 1001, "backend_workflow_id": 2001,
            "backend_run_url": self.api.payload["backend_run_url"],
            "artifact_id": 4001, "artifact_digest": "sha256:" + sha(self.api.archive),
            "artifact_size_in_bytes": len(self.api.archive),
            "artifact_created_at": self.api.artifact["created_at"],
            "artifact_updated_at": self.api.artifact["updated_at"],
            "artifact_expires_at": self.api.artifact["expires_at"],
            "archive_sha256": sha(self.api.archive), "manifest_sha256": sha(self.api.manifest_raw),
        })
        before = {p.name: (p.read_bytes(), p.stat().st_mtime_ns) for p in output.iterdir()}
        self.assertEqual(verifier.recheck(output, self.api, self.payload, self.consumer), provenance)
        self.assertEqual(before, {p.name: (p.read_bytes(), p.stat().st_mtime_ns) for p in output.iterdir()})
        self.assertEqual(self.api.run_reads, 4)
        self.assertEqual(self.api.artifact_reads, 4)
        self.assertEqual(sum(path.endswith("/zip") for path in self.api.calls), 2)

    def test_cli_outputs_only_provenance(self):
        output = self.root / "verified"
        for args in (["fetch", "--output", str(output)], ["recheck", "--input", str(output)]):
            with self.subTest(args=args), patch.dict(os.environ, self.env, clear=True), \
                    patch.object(verifier, "GitHub", return_value=self.api), \
                    contextlib.redirect_stdout(io.StringIO()) as stdout, \
                    contextlib.redirect_stderr(io.StringIO()) as stderr:
                self.assertEqual(verifier.main(args), 0)
                self.assertEqual(json.loads(stdout.getvalue())["artifact_id"], 4001)
                self.assertEqual(stderr.getvalue(), "")

    def test_event_ref_forms_for_both_branches(self):
        for branch in ("main", "unstable"):
            self.configure_branch(branch)
            for ref in (branch, f"refs/heads/{branch}"):
                with self.subTest(branch=branch, ref=ref):
                    self.event["ref"] = ref
                    self.write_event()
                    self.assertEqual(verifier.context(self.env), (self.payload, self.consumer))

    def test_invalid_refs_for_both_branches(self):
        for branch, other in (("main", "unstable"), ("unstable", "main")):
            self.configure_branch(branch)
            for ref in (other, f"refs/heads/{other}", f"refs/tags/{branch}",
                        "b" * 40, "", None, [], f"refs/heads/{branch}\n"):
                with self.subTest(branch=branch, event_ref=ref):
                    self.event["ref"] = ref
                    self.write_event()
                    with self.assertRaises(verifier.VerificationError):
                        verifier.context(self.env)
            for event_ref in (branch, f"refs/heads/{branch}"):
                self.event["ref"] = event_ref
                self.write_event()
                for ref in (branch, other, f"refs/heads/{other}", f"refs/tags/{branch}",
                            "b" * 40, "", None):
                    with self.subTest(branch=branch, event_ref=event_ref, cli_ref=ref), \
                            self.assertRaises(verifier.VerificationError):
                        verifier.context({**self.env, "GITHUB_REF": ref})

    def test_environment_rejections(self):
        for field, value in (
            ("GITHUB_EVENT_NAME", "repository_dispatch"), ("GITHUB_EVENT_NAME", "push"),
            ("GITHUB_REPOSITORY", "fork/cli"), ("GITHUB_REF", "refs/heads/main"),
            ("GITHUB_REF", "refs/heads/feature"), ("GITHUB_REF", "refs/tags/unstable"),
            ("GITHUB_SHA", "a\n"),
            ("GITHUB_RUN_ID", "0"), ("GITHUB_RUN_ATTEMPT", "01"), ("GITHUB_EVENT_PATH", ""),
        ):
            with self.subTest(field=field), self.assertRaises(verifier.VerificationError):
                verifier.context({**self.env, field: value})

    def test_event_rejections(self):
        mutations = [
            lambda e: e.update(ref="main"),
            lambda e: e.update(ref="refs/heads/main"),
            lambda e: e.pop("ref"),
            lambda e: e.update(inputs=None),
            lambda e: e.pop("inputs"),
            lambda e: e["repository"].update(fork=True),
            lambda e: e["repository"].update(full_name="fork/cli"),
            lambda e: e["repository"].update(default_branch="unstable"),
            lambda e: e["repository"].update(id=True),
            lambda e: e["inputs"].update(extra="input"),
            lambda e: e["inputs"].update(backend_run_id="3001"),
            lambda e: e["inputs"].update(upstream_run_id=3001),
            lambda e: e["inputs"].update(upstream_sha="x" * 40),
            lambda e: e["inputs"].update(upstream_repo="fork/Backend"),
            lambda e: e["inputs"].update(upstream_workflow="pipeline.yml"),
            lambda e: e["inputs"].update(upstream_workflow=".github/workflows/build-release.yml"),
            lambda e: e["inputs"].update(backend_branch="main"),
            lambda e: e["inputs"].update(backend_branch="feature"),
            lambda e: e["inputs"].update(backend_run_attempt="2/attempts/1"),
            lambda e: e["inputs"].update(upstream_run_url="https://evil.example/"),
            lambda e: e["inputs"].update(upstream_run_url="https://github.com/Jetscale-ai/Backend/actions/runs/3002"),
            lambda e: e["inputs"].update(spec_version="version\ncommand"),
            lambda e: e["inputs"].update(spec_sha="x" * 64),
            lambda e: e["inputs"].update(artifact_name="latest"),
        ]
        original = copy.deepcopy(self.event)
        for mutate in mutations:
            self.event = copy.deepcopy(original)
            mutate(self.event)
            self.write_event()
            with self.subTest(event=self.event), self.assertRaises(verifier.VerificationError):
                verifier.context(self.env)

    def test_exact_ten_string_inputs_required(self):
        original = copy.deepcopy(self.event)
        self.assertEqual(set(original["inputs"]), {
            "upstream_repo", "upstream_workflow", "upstream_run_id", "upstream_run_url",
            "upstream_sha", "backend_run_attempt", "backend_branch", "spec_sha",
            "spec_version", "artifact_name",
        })
        for key in original["inputs"]:
            for mode in ("missing", "number", "boolean", "null"):
                self.event = copy.deepcopy(original)
                if mode == "missing":
                    self.event["inputs"].pop(key)
                else:
                    self.event["inputs"][key] = {"number": 1, "boolean": True, "null": None}[mode]
                self.write_event()
                with self.subTest(key=key, mode=mode), self.assertRaises(verifier.VerificationError):
                    verifier.context(self.env)
        self.assertEqual(self.api.calls, [])

    def test_repository_casing_is_accepted(self):
        self.event["inputs"]["upstream_repo"] = "JETSCALE-AI/backend"
        self.event["repository"]["full_name"] = "JETSCALE-AI/CLI"
        self.env["GITHUB_REPOSITORY"] = "jetscale-ai/CLI"
        self.write_event()
        self.assertEqual(verifier.context(self.env), (self.payload, self.consumer))

    def test_legacy_dispatch_payload_rejected(self):
        self.event.pop("inputs")
        self.event.update(action="unstable-openapi-spec-updated", client_payload=self.api.payload)
        self.write_event()
        for event_name in ("repository_dispatch", "workflow_dispatch"):
            with self.subTest(event_name=event_name), self.assertRaises(verifier.VerificationError):
                verifier.context({**self.env, "GITHUB_EVENT_NAME": event_name})

    def test_branch_lineage_rejections(self):
        for branch, other in (("main", "unstable"), ("unstable", "main")):
            for target in ("cli_ref", "event_ref", "run", "artifact", "manifest"):
                with self.subTest(branch=branch, target=target):
                    self.configure_branch(branch)
                    if target == "cli_ref":
                        self.env["GITHUB_REF"] = f"refs/heads/{other}"
                    elif target == "event_ref":
                        self.event["ref"] = other
                    elif target == "run":
                        self.api.run["head_branch"] = other
                    elif target == "artifact":
                        self.api.artifact["workflow_run"]["head_branch"] = other
                    else:
                        self.api.manifest["backend_branch"] = other
                        self.api.refresh_archive()
                    self.write_event()
                    with self.assertRaises(verifier.VerificationError):
                        payload, consumer = verifier.context(self.env)
                        verifier.verify(self.api, payload, consumer)

    def test_consumer_lineage_rejections_before_provider_reads(self):
        for field, value in (("cli_event", "repository_dispatch"),
                             ("dispatch_event", "unstable-openapi-spec-updated"),
                             ("cli_ref", "refs/heads/main")):
            with self.subTest(field=field), self.assertRaises(verifier.VerificationError):
                verifier.verify(self.api, self.payload, {**self.consumer, field: value})
        with self.assertRaises(verifier.VerificationError):
            verifier.verify(self.api, {**self.payload, "backend_branch": "feature"}, self.consumer)
        self.assertEqual(self.api.calls, [])

    def test_run_rejections(self):
        for field, value in (
            ("id", 3002), ("run_attempt", 3), ("run_attempt", True), ("head_sha", "c" * 40),
            ("status", "in_progress"), ("conclusion", "failure"), ("event", "pull_request"),
            ("head_branch", "main"), ("path", ".github/workflows/build-release.yml"),
            ("name", "Other"), ("workflow_id", 2002),
            ("repository", {"id": 1001, "full_name": "Jetscale-ai/Backend", "fork": True}),
            ("head_repository", {"id": 999, "full_name": "Jetscale-ai/Backend", "fork": False}),
            ("head_repository", {"id": 1001, "full_name": "other/Backend", "fork": False}),
        ):
            with self.subTest(field=field, value=value):
                self.api = FixtureAPI()
                self.api.run[field] = value
                with self.assertRaises(verifier.VerificationError):
                    self.verify()

    def test_authoritative_repository_and_workflow(self):
        for target, field, value in (("repo", "id", 999), ("repo", "fork", True),
                                     ("workflow", "id", 999), ("workflow", "path", "other.yml")):
            with self.subTest(target=target, field=field):
                self.api = FixtureAPI()
                getattr(self.api, target)[field] = value
                with self.assertRaises(verifier.VerificationError):
                    self.verify()

    def test_rerun_during_download_rejected_without_outputs(self):
        def rerun(api):
            if api.run_reads == 2:
                api.run["run_attempt"] = 3
        self.api.on_run_read = rerun
        output = self.root / "verified"
        with self.assertRaisesRegex(verifier.VerificationError, "attempt changed"):
            verifier.fetch(output, self.api, self.payload, self.consumer)
        self.assertFalse(output.exists())
        self.assertEqual({p.name for p in self.root.iterdir()}, {"event.json"})

    def test_artifact_rejections(self):
        for field, value in (("id", True), ("expired", True), ("digest", None),
                             ("size_in_bytes", verifier.ARCHIVE_LIMIT + 1),
                             ("size_in_bytes", True), ("updated_at", "unsafe\ntext")):
            with self.subTest(field=field):
                self.api = FixtureAPI()
                self.api.artifact[field] = value
                with self.assertRaises(verifier.VerificationError):
                    self.verify()
        for field, value in (("id", 3002), ("head_sha", "c" * 40), ("head_branch", "main"),
                             ("repository_id", 999), ("head_repository_id", 999)):
            with self.subTest(origin_field=field):
                self.api = FixtureAPI()
                self.api.artifact["workflow_run"][field] = value
                with self.assertRaises(verifier.VerificationError):
                    self.verify()

    def test_inventory_exact_not_latest(self):
        for inventory in ([], [self.api.artifact, {**self.api.artifact, "id": 4002}],
                          [self.api.artifact, self.api.artifact]):
            self.api.inventory = inventory
            with self.subTest(count=len(inventory)), self.assertRaises(verifier.VerificationError):
                self.verify()

    def test_bounded_pagination(self):
        self.api.inventory = [{"id": n, "name": "other"} for n in range(1, 101)] + [self.api.artifact]
        self.verify()
        self.assertTrue(any(path.endswith("page=2") for path in self.api.calls))
        for pages in (
            [{"total_count": 1001, "artifacts": []}],
            [{"total_count": 2, "artifacts": [self.api.artifact]}],
            [{"total_count": 101, "artifacts": self.api.inventory[:100]}, {"total_count": 102, "artifacts": []}],
        ):
            self.api.pages = pages
            with self.subTest(pages=len(pages)), self.assertRaises(verifier.VerificationError):
                self.verify()

    def test_metadata_race_and_download_hash(self):
        def change(api):
            if api.artifact_reads == 2:
                api.artifact["digest"] = "sha256:" + "0" * 64
        self.api.on_artifact_read = change
        with self.assertRaisesRegex(verifier.VerificationError, "metadata changed"):
            self.verify()
        self.api = FixtureAPI()
        self.api.archive += b"changed"
        with self.assertRaisesRegex(verifier.VerificationError, "archive hash or size"):
            self.verify()

    def test_manifest_exact_fields_and_types(self):
        original = copy.deepcopy(self.api.manifest)
        changes = {key: "wrong" for key in original}
        changes.update(schema_version=True, backend_run_id=3001, backend_run_attempt="1", extra="not allowed")
        for field, value in changes.items():
            with self.subTest(field=field):
                self.api.manifest = {**original, field: value}
                self.api.refresh_archive()
                with self.assertRaisesRegex(verifier.VerificationError, "manifest"):
                    self.verify()
        self.api.manifest = {k: v for k, v in original.items() if k != "backend_event"}
        self.api.refresh_archive()
        with self.assertRaises(verifier.VerificationError):
            self.verify()

    def test_raw_hash_and_api_version(self):
        self.payload["spec_sha"] = "0" * 64
        with self.assertRaisesRegex(verifier.VerificationError, "Raw schema hash"):
            self.verify()
        self.payload["spec_sha"] = sha(self.api.raw)
        self.payload["spec_version"] = "other"
        with self.assertRaisesRegex(verifier.VerificationError, "API version"):
            self.verify()

    def test_zip_member_rejections(self):
        for members in (
            [("../openapi.json", b"{}"), ("openapi-manifest.json", b"{}")],
            [("/openapi.json", b"{}"), ("openapi-manifest.json", b"{}")],
            [("openapi.json", b"{}"), ("openapi.json", b"{}"), ("openapi-manifest.json", b"{}")],
            [("openapi.json", b"{}"), ("openapi-manifest.json", b"{}"), ("script.py", b"pass")],
            [("openapi.json", b"{}"), ("openapi-manifest.json", b"x" * (verifier.MANIFEST_LIMIT + 1))],
        ):
            with self.subTest(names=[m[0] for m in members]):
                self.api.refresh_archive(members)
                with self.assertRaises(verifier.VerificationError):
                    self.verify()
        symlink = zipfile.ZipInfo("openapi.json")
        symlink.create_system = 3
        symlink.external_attr = (stat.S_IFLNK | 0o777) << 16
        with self.assertRaisesRegex(verifier.VerificationError, "Unsafe ZIP"):
            verifier.unpack(archive_for(b"{}", b"{}", [(symlink, b"/tmp/input"), ("openapi-manifest.json", b"{}")]))
        with self.assertRaises(verifier.VerificationError):
            verifier.unpack(b"not a zip")
        with patch.object(verifier, "SCHEMA_LIMIT", 1), self.assertRaises(verifier.VerificationError):
            verifier.unpack(archive_for(b"{}", b"{}"))

    def test_zip_directory_bounds_and_corrupt_deflate(self):
        archive = bytearray(self.api.archive)
        end = archive.rfind(b"PK\x05\x06")
        struct.pack_into("<I", archive, end + 12, 65537)
        with patch.object(verifier.zipfile, "ZipFile") as parser, self.assertRaises(verifier.VerificationError):
            verifier.unpack(bytes(archive))
        parser.assert_not_called()
        archive = bytearray(self.api.archive)
        name_length, extra_length = struct.unpack_from("<HH", archive, 26)
        archive[30 + name_length + extra_length] = 7  # Invalid DEFLATE block type.
        with self.assertRaisesRegex(verifier.VerificationError, "Invalid schema ZIP"):
            verifier.unpack(bytes(archive))

    def test_atomic_output_cleanup_on_io_failure(self):
        output = self.root / "verified"
        original = Path.write_bytes

        def fail_manifest(path, content):
            if path.name == "manifest.json":
                raise OSError("private diagnostic")
            return original(path, content)

        with patch.object(Path, "write_bytes", fail_manifest), self.assertRaises(OSError):
            verifier.fetch(output, self.api, self.payload, self.consumer)
        self.assertEqual({p.name for p in self.root.iterdir()}, {"event.json"})

    def test_schema_directed_inputs_rejected(self):
        unsafe = [
            {"$ref": value} for value in (
                "https://example.com/schema", "//example.com/schema", "file:///etc/passwd",
                "other.json#/Item", "/tmp/schema", "", 123, "#anchor",
            )
        ] + [{"x-go-type": "injected"}, {"x-go-type-import": {"path": "evil/package"}},
             {"x-oapi-codegen-extra-tags": {"yaml": "injected"}}, {"X-Go-Name": "injected"},
             {"x-logo": {}}, {"$id": "https://example.com/"}, {"$dynamicRef": "#/Item"},
             {"$recursiveRef": "#"}, {"$schema": "https://example.com/meta"},
             {"operationRef": "https://example.com/openapi.json"}]
        for entry in unsafe:
            with self.subTest(entry=entry):
                schema = {**self.api.schema, "components": {"schemas": {"Item": entry}}}
                raw = json.dumps(schema).encode()
                with self.assertRaises(verifier.VerificationError):
                    verifier.safe_schema(raw, {**self.payload, "spec_sha": sha(raw)})

    def check_schema(self, schema):
        raw = json.dumps(schema).encode()
        verifier.safe_schema(raw, {**self.payload, "spec_sha": sha(raw)})

    def test_real_marketplace_property_and_named_maps(self):
        # Minimal regression from raw-openapi.json's
        # Body_fulfill_api_v2_aws_marketplace_action_fulfill_post schema.
        property_schema = {"anyOf": [{"type": "string"}, {"type": "null"}],
                           "title": "X-Amzn-Marketplace-Token"}
        schema = copy.deepcopy(self.api.schema)
        schema["components"]["schemas"]["Marketplace"] = {
            "properties": {"x-amzn-marketplace-token": property_schema}, "type": "object",
        }
        schema["components"]["schemas"]["x-named-schema"] = {"type": "string"}
        schema["components"]["headers"] = {"x-request-id": {"schema": {"type": "string"}}}
        schema["components"]["parameters"] = {
            "x-parameter": {"name": "x-header", "in": "header", "schema": {"type": "string"}},
        }
        schema["components"]["responses"] = {"x-response": {"description": "OK"}}
        schema["paths"] = {"/items": {"get": {"responses": {"200": {
            "description": "OK", "headers": {"x-request-id": {"schema": {"type": "string"}}},
        }}}}}
        self.check_schema(schema)
        # Keyword-looking names remain names, including beneath schema maps.
        for name in ("$ref", "examples", "properties", "default", "x-go-type"):
            with self.subTest(name=name):
                schema["components"]["schemas"]["Marketplace"]["properties"] = {name: {"type": "string"}}
                self.check_schema(schema)

    def test_literal_examples_defaults_and_enum_are_not_codegen_inputs(self):
        data = {"$ref": "https://example.com/not-a-schema", "x-go-type": "literal data",
                "nested": [{"$id": "literal", "x-go-type-import": {"path": "not/an/import"}}]}
        schema = copy.deepcopy(self.api.schema)
        schema["components"]["schemas"]["Item"] = {
            "type": "object", "example": data, "examples": [data],
            "default": data, "enum": [data], "const": data,
        }
        schema["components"]["examples"] = {"x-example": {"value": data}}
        schema["components"]["links"] = {
            "x-link": {"operationId": "getItem", "parameters": data, "requestBody": data},
        }
        schema["paths"] = {"/items": {"get": {"responses": {"200": {
            "description": "OK", "content": {"application/json": {
                "example": data, "examples": {"x-example": {"value": data}},
            }},
        }}}}}
        self.check_schema(schema)

    def test_map_values_and_schema_composition_keep_strict_checks(self):
        for unsafe in ({"$ref": "https://example.com/schema"}, {"x-go-type": "injected"}):
            locations = [
                {"components": {"schemas": {"x-item": unsafe}}},
                {"components": {"schemas": {"Item": {"properties": {"examples": unsafe}}}}},
                {"components": {"schemas": {"Item": {"items": unsafe}}}},
                {"components": {"schemas": {"Item": {"additionalProperties": unsafe}}}},
                {"components": {"schemas": {"Item": {"allOf": [unsafe]}}}},
                {"components": {"headers": {"x-header": unsafe}}},
                {"components": {"examples": {"x-example": unsafe}}},
                {"components": {"examples": {"x-example": {"value": {}, **unsafe}}}},
                {"components": {"callbacks": {"x-callback": unsafe}}},
                {"paths": {"/items": {"get": {"responses": {"default": unsafe}}}}},
                {"paths": {"/items": {"get": {"responses": {"200": {
                    "description": "OK", "content": {"application/json": {
                        "examples": {"x-example": unsafe},
                    }},
                }}}}}},
            ]
            for index, location in enumerate(locations):
                with self.subTest(unsafe=unsafe, location=index), self.assertRaises(verifier.VerificationError):
                    self.check_schema({**self.api.schema, **location})

    def test_local_references_cannot_launder_example_data(self):
        for unsafe in ({"$ref": "https://example.com/schema"}, {"x-go-type": "injected"}):
            schema = copy.deepcopy(self.api.schema)
            schema["components"]["examples"] = {"payload": {"value": unsafe}}
            schema["components"]["schemas"]["Item"] = {"$ref": "#/components/examples/payload/value"}
            with self.subTest(unsafe=unsafe), self.assertRaises(verifier.VerificationError):
                self.check_schema(schema)

    def test_local_reference_cycles_and_pointer_escaping(self):
        schema = copy.deepcopy(self.api.schema)
        schema["components"]["schemas"] = {
            "Item": {"$ref": "#/components/schemas/x~1item"},
            "x/item": {"type": "object", "properties": {"next": {"$ref": "#/components/schemas/Item"}}},
        }
        self.check_schema(schema)

    def discriminator_schema(self, other_name="Other", union="oneOf"):
        schema = copy.deepcopy(self.api.schema)
        components = schema["components"]["schemas"]
        variant = {"type": "object", "required": ["kind"], "properties": {"kind": {"type": "string"}}}
        components["Item"] = copy.deepcopy(variant)
        components[other_name] = copy.deepcopy(variant)
        escaped = other_name.replace("~", "~0").replace("/", "~1")
        components["Choice"] = {
            union: [{"$ref": "#/components/schemas/Item"}, {"$ref": "#/components/schemas/" + escaped}],
            "discriminator": {"propertyName": "kind"},
        }
        return schema

    def test_discriminator_mapping_literal_injection_rejected(self):
        for literal in ('quoted"value', "slash/value", "back\\slash", "line\nbreak", "line\rbreak",
                        "tab\tvalue", "nul\x00value", "`tag`", "semi;colon", "two words", "café", "", "a" * 129):
            with self.subTest(literal=repr(literal)):
                schema = self.discriminator_schema()
                schema["components"]["schemas"]["Choice"]["discriminator"]["mapping"] = {
                    literal: "#/components/schemas/Item", "other": "#/components/schemas/Other",
                }
                with self.assertRaisesRegex(verifier.VerificationError, "Unsafe discriminator mapping literal"):
                    self.check_schema(schema)

    def test_implicit_discriminator_names_rejected_with_partial_mapping(self):
        for union in ("oneOf", "anyOf"):
            for name in ('quoted"Name', "slash/Name", "back\\Name", "line\nName", "`Name`", "%22Name", "a" * 129):
                for mapping in (None, {}, {"item": "#/components/schemas/Item"}):
                    with self.subTest(union=union, name=repr(name), mapping=mapping):
                        schema = self.discriminator_schema(name, union)
                        discriminator = schema["components"]["schemas"]["Choice"]["discriminator"]
                        if mapping is not None:
                            discriminator["mapping"] = mapping
                        with self.assertRaisesRegex(verifier.VerificationError, "Unsafe implicit discriminator literal"):
                            self.check_schema(schema)
        # Keep the same guard when a discriminator may be attached by merging.
        schema = self.discriminator_schema('quoted"Name')
        schema["components"]["schemas"]["Choice"].pop("discriminator")
        with self.assertRaisesRegex(verifier.VerificationError, "Unsafe implicit discriminator literal"):
            self.check_schema(schema)

    def test_discriminator_property_name_and_shape_rejected(self):
        for property_name in ('kind"', "kind`", "kind\\", "kind\n", "", "a" * 129, None):
            schema = self.discriminator_schema()
            schema["components"]["schemas"]["Choice"]["discriminator"]["propertyName"] = property_name
            with self.subTest(property_name=repr(property_name)), self.assertRaisesRegex(
                verifier.VerificationError, "Unsafe discriminator property name"
            ):
                self.check_schema(schema)
        for mapping in ([], None, {"item": {"$ref": "#/components/schemas/Item"}}):
            schema = self.discriminator_schema()
            schema["components"]["schemas"]["Choice"]["discriminator"]["mapping"] = mapping
            with self.subTest(mapping=mapping), self.assertRaises(verifier.VerificationError):
                self.check_schema(schema)

    def test_valid_explicit_implicit_and_real_focus_discriminators(self):
        for union in ("oneOf", "anyOf"):
            schema = self.discriminator_schema("x-Other.v1:variant", union)
            self.check_schema(schema)
            schema["components"]["schemas"]["Choice"]["discriminator"]["mapping"] = {
                "a" * 128: "#/components/schemas/Item",
                "x-other.v1:variant": "#/components/schemas/x-Other.v1:variant",
            }
            self.check_schema(schema)
        # Actual mapping from raw-openapi.json's provider_config union; no
        # dependency on an ignored local export is needed to run this regression.
        schema = copy.deepcopy(self.api.schema)
        mapping = {
            "focus_azure_blob": "#/components/schemas/AzureBlobCostDataSourceWriteConfig",
            "focus_bigquery": "#/components/schemas/GcpBigQueryCostDataSourceWriteConfig",
            "focus_s3": "#/components/schemas/AwsS3CostDataSourceWriteConfig",
        }
        for reference in mapping.values():
            schema["components"]["schemas"][reference.rsplit("/", 1)[-1]] = {
                "type": "object", "required": ["source_kind"],
                "properties": {"source_kind": {"type": "string"}},
            }
        schema["components"]["schemas"]["CloudCostDataSourceUpsertRequest"] = {
            "type": "object", "properties": {"provider_config": {
                "oneOf": [{"$ref": reference} for reference in mapping.values()],
                "discriminator": {"propertyName": "source_kind", "mapping": mapping},
            }},
        }
        self.check_schema(schema)

    def test_discriminator_shaped_literal_data_stays_literal(self):
        schema = copy.deepcopy(self.api.schema)
        data = {"discriminator": {"propertyName": "quote\"", "mapping": {"quote\"": "literal"}},
                "oneOf": [{"$ref": "https://example.com/quoted\"name"}]}
        schema["components"]["schemas"]["Item"] = {
            "type": "object", "properties": {"discriminator": {"type": "string"}},
            "example": data, "default": data, "enum": [data],
        }
        schema["components"]["examples"] = {"payload": {"value": data}}
        self.check_schema(schema)
        # A local ref promotes literal data into schema/codegen context.
        schema["components"]["schemas"]["Item"] = {"$ref": "#/components/examples/payload/value"}
        with self.assertRaises(verifier.VerificationError):
            self.check_schema(schema)

    def test_schema_depth_and_structure(self):
        nested = {}
        for _ in range(66):
            nested = {"nested": nested}
        for change in ({"openapi": "2.0"}, {"paths": []}, {"info": None}, {"extra": nested}):
            raw = json.dumps({**self.api.schema, **change}).encode()
            with self.subTest(change=list(change)), self.assertRaises(verifier.VerificationError):
                verifier.safe_schema(raw, {**self.payload, "spec_sha": sha(raw)})

    def test_strict_json(self):
        for raw in (b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":Infinity}', b'{"a":1e9999}', b'\xff', b'[' * 2000):
            with self.subTest(raw=raw[:30]), self.assertRaises(verifier.VerificationError):
                verifier.parse_json(raw)

    def test_recheck_rejects_local_or_remote_changes(self):
        output = self.root / "verified"
        verifier.fetch(output, self.api, self.payload, self.consumer)
        for name in verifier.FILES:
            path = output / name
            original = path.read_bytes()
            path.write_bytes(b"{}")
            with self.subTest(file=name), self.assertRaises(verifier.VerificationError):
                verifier.recheck(output, self.api, self.payload, self.consumer)
            path.write_bytes(original)
        for mutate in (
            lambda: self.api.run.update(run_attempt=3),
            lambda: self.api.artifact.update(expired=True),
            lambda: self.consumer.update(cli_run_attempt="2"),
        ):
            mutate()
            with self.assertRaises(verifier.VerificationError):
                verifier.recheck(output, self.api, self.payload, self.consumer)
            self.api = FixtureAPI()
        self.consumer["cli_run_attempt"] = "1"
        self.api.artifact["id"] = 4002
        with self.assertRaisesRegex(verifier.VerificationError, "Saved provenance"):
            verifier.recheck(output, self.api, self.payload, self.consumer)
        self.assertTrue(any("/artifacts/4002/zip" in path for path in self.api.calls))

    def test_recheck_rejects_extra_files_and_provenance_extensions(self):
        output = self.root / "verified"
        provenance = verifier.fetch(output, self.api, self.payload, self.consumer)
        (output / "generated.go").write_text("not verifier evidence")
        with self.assertRaisesRegex(verifier.VerificationError, "directory members"):
            verifier.recheck(output, self.api, self.payload, self.consumer)
        (output / "generated.go").unlink()
        for altered in ({**provenance, "extra": "unverified"}, {**provenance, "schema_version": True}):
            (output / "provenance.json").write_text(json.dumps(altered))
            with self.assertRaisesRegex(verifier.VerificationError, "Saved provenance"):
                verifier.recheck(output, self.api, self.payload, self.consumer)

    def test_existing_output_and_symlinks_are_not_overwritten(self):
        output = self.root / "verified"
        output.mkdir()
        (output / "human.txt").write_text("keep")
        with self.assertRaises(verifier.VerificationError):
            verifier.fetch(output, self.api, self.payload, self.consumer)
        self.assertEqual(self.api.calls, [])
        self.assertEqual((output / "human.txt").read_text(), "keep")
        target = self.root / "target"
        verifier.fetch(target, self.api, self.payload, self.consumer)
        (target / "openapi.json").unlink()
        (target / "openapi.json").symlink_to(self.root / "event.json")
        with self.assertRaises(OSError):
            verifier.recheck(target, self.api, self.payload, self.consumer)
        with patch.object(verifier, "JSON_LIMIT", 1), self.assertRaises(verifier.VerificationError):
            verifier.context(self.env)

    def test_failure_and_interrupt_stream_contract(self):
        for error, code, message in ((verifier.VerificationError("Backend run attempt changed"), 1, "attempt changed"),
                                     (OSError("secret-token"), 1, "bounded verification inputs"),
                                     (KeyboardInterrupt(), 130, "interrupted")):
            with self.subTest(code=code), patch.dict(os.environ, self.env, clear=True), \
                    patch.object(verifier, "verify", side_effect=error), \
                    contextlib.redirect_stdout(io.StringIO()) as stdout, \
                    contextlib.redirect_stderr(io.StringIO()) as stderr:
                self.assertEqual(verifier.main(["fetch", "--output", str(self.root / "new")]), code)
                self.assertEqual(stdout.getvalue(), "")
                self.assertIn(message, stderr.getvalue())
                self.assertNotIn("secret-token", stderr.getvalue())
                self.assertNotIn("\x1b", stderr.getvalue())


class GitHubProcessTests(unittest.TestCase):
    """Use tiny local Python children in place of gh to test real pipe bounds."""

    def child(self, source):
        original = subprocess.Popen

        def spawn(command, **kwargs):
            self.command = command
            self.options = kwargs
            self.process = original([sys.executable, "-c", source], **kwargs)
            return self.process
        return patch.object(verifier.subprocess, "Popen", side_effect=spawn)

    def request(self, limit=100):
        return verifier.GitHub().get("repos/Jetscale-ai/Backend/actions/runs/3001", limit)

    def test_bounded_request_and_safe_environment(self):
        with patch.dict(os.environ, {"GH_TOKEN": "test-only", "GH_DEBUG": "api", "GH_HOST": "evil.example",
                                     "GITHUB_TOKEN": "other-token"}), self.child("print('{}')"):
            self.assertEqual(self.request(), b"{}\n")
        self.assertEqual(self.command[:7], ["gh", "api", "--hostname", "github.com", "--method", "GET", "-H"])
        self.assertIs(self.options["stderr"], subprocess.DEVNULL)
        self.assertIs(self.options["stdin"], subprocess.DEVNULL)
        self.assertTrue(self.options["start_new_session"])
        self.assertNotIn("GH_DEBUG", self.options["env"])
        self.assertNotIn("GITHUB_TOKEN", self.options["env"])
        self.assertEqual(self.options["env"]["GH_HOST"], "github.com")

    def test_size_timeout_and_child_failure_are_sanitized(self):
        for source, limit, timeout, expected in (
            ("import os; os.write(1, b'x' * 10000)", 100, 30, "size limit"),
            ("import time; time.sleep(10)", 100, 0.05, "time limit"),
            ("import os, time; os.close(1); time.sleep(10)", 100, 0.05, "time limit"),
            ("import sys; print('secret-token', file=sys.stderr); print('secret-token'); sys.exit(7)", 100, 30, "request failed"),
        ):
            with self.subTest(expected=expected), patch.dict(os.environ, {"GH_TOKEN": "test-only"}), \
                    patch.object(verifier, "CALL_SECONDS", timeout), self.child(source), \
                    self.assertRaisesRegex(verifier.VerificationError, expected) as error:
                self.request(limit)
            self.assertNotIn("secret-token", str(error.exception))
            self.assertIsNotNone(self.process.poll())

    def test_interrupt_reaps_child(self):
        with patch.dict(os.environ, {"GH_TOKEN": "test-only"}), self.child("import time; time.sleep(10)"), \
                patch.object(verifier.selectors.DefaultSelector, "select", side_effect=KeyboardInterrupt), \
                self.assertRaises(KeyboardInterrupt):
            self.request()
        self.assertIsNotNone(self.process.poll())

    def test_missing_token_exhausted_budget_and_wrong_host_do_not_spawn(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(verifier.subprocess, "Popen") as spawn:
            with self.assertRaises(verifier.VerificationError):
                self.request()
            spawn.assert_not_called()
        with patch.dict(os.environ, {"GH_TOKEN": "test-only"}), patch.object(verifier.subprocess, "Popen") as spawn:
            api = verifier.GitHub()
            api.deadline = 0
            with self.assertRaises(verifier.VerificationError):
                api.get("repos/Jetscale-ai/Backend/actions/runs/3001")
            with self.assertRaises(verifier.VerificationError):
                api.get("https://evil.example/")
            spawn.assert_not_called()


if __name__ == "__main__":
    unittest.main()
