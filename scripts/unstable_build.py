#!/usr/bin/env python3
"""Verify Backend's branch-matched schema without generating code or publishing anything.

fetch --output DIR requires a new directory beneath an existing parent and writes
only openapi.json, manifest.json (the producer's openapi-manifest.json), and
provenance.json. recheck --input DIR is read-only and must run in the same CLI
run/attempt, immediately before publication. Both commands print provenance JSON
on success, or a sanitized diagnostic on stderr and exit 1 on rejection (2 for
usage, 130 for interruption). Neither command retries.

GH_TOKEN is required only while verifying; never pass it to schema processing or
code generation. Run the pinned generator with trusted configuration, templates,
and no network/credentials. This gate rejects all x-* extensions on OpenAPI/schema
objects, but not user-defined map names or literal example/default/enum data.
The verifier does not attest generated code, the checkout, or a packaged binary.
"""

import argparse
import hashlib
import io
import json
import math
import os
from pathlib import Path
import re
import selectors
import signal
import stat
import struct
import subprocess
import sys
import tempfile
import time
import zipfile
import zlib
from urllib.parse import unquote


BACKEND = "Jetscale-ai/Backend"
CLI = "Jetscale-ai/cli"
EVENT = "openapi-schema-build"
CHANNELS = {"main": ("live", "jetscale"), "unstable": ("unstable", "jetscale-unstable")}
WORKFLOW = ".github/workflows/pipeline.yml"
WORKFLOW_NAME = "Build & Release"
ARTIFACT = "openapi-spec"
ARCHIVE_LIMIT = 32 * 1024 * 1024
SCHEMA_LIMIT = 32 * 1024 * 1024
MANIFEST_LIMIT = 16 * 1024
JSON_LIMIT = 2 * 1024 * 1024
PAGE_SIZE = 100
MAX_PAGES = 10
CALL_SECONDS = 30
TOTAL_SECONDS = 300
PAYLOAD_KEYS = {
    "backend_sha", "backend_run_id", "backend_run_attempt", "spec_sha",
    "spec_version", "artifact_name", "backend_run_url", "backend_branch",
}
INPUT_KEYS = {
    "upstream_repo", "upstream_workflow", "upstream_run_id", "upstream_run_url",
    "upstream_sha", "backend_run_attempt", "backend_branch", "spec_sha",
    "spec_version", "artifact_name",
}
FILES = {"openapi.json", "manifest.json", "provenance.json"}


class VerificationError(Exception):
    """Messages are static: never include untrusted data or child diagnostics."""


def require(condition, message):
    if not condition:
        raise VerificationError(message)


def object_value(value):
    require(isinstance(value, dict), "Expected a JSON object")
    return value


def positive_id(value):
    require(type(value) is int and 0 < value < 2**63, "Invalid numeric ID")
    return value


def identifier(value):
    require(isinstance(value, str) and re.fullmatch(r"[1-9][0-9]{0,18}", value),
            "Invalid string run ID or attempt")
    positive_id(int(value))
    return value


def build_identity(branch, run_id, attempt):
    """Return channel, binary, and version for a branch and string run coordinates."""
    require(isinstance(branch, str) and branch in CHANNELS, "Unsupported build branch")
    identifier(run_id)
    identifier(attempt)
    channel, binary = CHANNELS[branch]
    return channel, binary, f"0.0.0-{branch}.{run_id}.{attempt}"


def matches(value, pattern, message):
    require(isinstance(value, str) and re.fullmatch(pattern, value), message)
    return value


def encoded(value):
    return (json.dumps(value, sort_keys=True, allow_nan=False) + "\n").encode("utf-8")


def same_json(actual, expected, message):
    # Python equality alone considers True == 1; the manifest contract does not.
    require(encoded(actual) == encoded(expected), message)


def parse_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "Duplicate JSON key")
            result[key] = value
        return result

    def invalid_constant(_value):
        raise VerificationError("Non-finite JSON number")

    def finite_float(value):
        number = float(value)
        require(math.isfinite(number), "Non-finite JSON number")
        return number

    try:
        return json.loads(raw.decode("utf-8"), object_pairs_hook=pairs,
                          parse_constant=invalid_constant, parse_float=finite_float)
    except (UnicodeError, ValueError, RecursionError):
        raise VerificationError("Invalid or excessively nested JSON") from None


def read_bounded(path, limit):
    # Refuse symlinks and special files, including FIFOs which could block reads.
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK), "rb") as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_size <= limit,
                "Input must be a bounded regular file")
        raw = stream.read(limit + 1)
    require(len(raw) <= limit, "Input exceeds size limit")
    return raw


class GitHub:
    """Fixed-host GET requests; bounded stdout in memory, stderr discarded at source."""

    def __init__(self):
        self.deadline = time.monotonic() + TOTAL_SECONDS

    def get(self, path, limit=JSON_LIMIT):
        require(path == f"repos/{BACKEND}" or path.startswith(f"repos/{BACKEND}/"),
                "Unexpected API destination")
        require(bool(os.environ.get("GH_TOKEN")), "GH_TOKEN is required for Backend Actions reads")
        timeout = min(CALL_SECONDS, self.deadline - time.monotonic())
        require(timeout > 0, "Verification time budget exhausted")
        # Do not inherit GH_DEBUG, GH_HOST, GITHUB_TOKEN, pagers, or interactive settings.
        env = {key: os.environ[key] for key in (
            "PATH", "HOME", "XDG_CONFIG_HOME", "SSL_CERT_FILE", "SSL_CERT_DIR",
            "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "GH_TOKEN",
        ) if key in os.environ}
        env.update(GH_PROMPT_DISABLED="1", GH_HOST="github.com", GH_PAGER="cat")
        command = ["gh", "api", "--hostname", "github.com", "--method", "GET",
                   "-H", "X-GitHub-Api-Version: 2022-11-28", path]
        end = time.monotonic() + timeout
        try:
            with subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                  stderr=subprocess.DEVNULL, env=env,
                                  start_new_session=True) as process:
                try:
                    output = bytearray()
                    with selectors.DefaultSelector() as selector:
                        selector.register(process.stdout, selectors.EVENT_READ)
                        while True:
                            remaining = end - time.monotonic()
                            require(remaining > 0, "GitHub request exceeded its time limit")
                            ready = selector.select(remaining)
                            require(bool(ready), "GitHub request exceeded its time limit")
                            chunk = os.read(process.stdout.fileno(), min(65536, limit + 1 - len(output)))
                            if not chunk:
                                break
                            output.extend(chunk)
                            require(len(output) <= limit, "GitHub response exceeds size limit")
                    status = process.wait(timeout=max(0.001, end - time.monotonic()))
                    require(status == 0, "GitHub request failed; check Backend Actions read access")
                    return bytes(output)
                finally:
                    # Also stop any child holding the pipe open on timeout/interruption.
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait(timeout=1)
        except subprocess.TimeoutExpired:
            raise VerificationError("GitHub request exceeded its time limit") from None
        except OSError:
            raise VerificationError("Could not execute GitHub CLI request") from None

    def json(self, path):
        return object_value(parse_json(self.get(path)))


def repository(value, expected, repo_id=None):
    value = object_value(value)
    name = value.get("full_name")
    require(isinstance(name, str) and name.lower() == expected.lower()
            and value.get("fork") is False, "Fork or repository mismatch")
    actual_id = positive_id(value.get("id"))
    require(repo_id is None or actual_id == repo_id, "Repository ID mismatch")
    return value


def context(env):
    require(env.get("GITHUB_EVENT_NAME") == "workflow_dispatch", "Expected workflow_dispatch")
    require(env.get("GITHUB_REPOSITORY", "").lower() == CLI.lower(), "Expected CLI repository")
    sha = matches(env.get("GITHUB_SHA"), r"[0-9a-f]{40}", "Invalid CLI SHA")
    run_id = identifier(env.get("GITHUB_RUN_ID"))
    attempt = identifier(env.get("GITHUB_RUN_ATTEMPT"))
    require(bool(env.get("GITHUB_EVENT_PATH")), "GITHUB_EVENT_PATH is required")
    event = object_value(parse_json(read_bounded(env["GITHUB_EVENT_PATH"], JSON_LIMIT)))
    repo = repository(event.get("repository"), CLI)
    require(repo.get("default_branch") == "main", "CLI default branch must be main")
    inputs = object_value(event.get("inputs"))
    require(set(inputs) == INPUT_KEYS and all(isinstance(v, str) for v in inputs.values()),
            "Unexpected dispatch payload fields or types")
    require(inputs["upstream_repo"].lower() == BACKEND.lower(), "Unexpected upstream repository")
    require(inputs["upstream_workflow"] == WORKFLOW, "Unexpected upstream workflow")
    branch = inputs["backend_branch"]
    build_identity(branch, run_id, attempt)
    require(env.get("GITHUB_REF") == f"refs/heads/{branch}"
            and event.get("ref") in (branch, f"refs/heads/{branch}"),
            "Dispatch and CLI refs must match the Backend branch")
    payload = {key: inputs[key] for key in PAYLOAD_KEYS
               if key not in {"backend_run_id", "backend_run_url", "backend_sha"}}
    payload.update(backend_run_id=inputs["upstream_run_id"],
                   backend_run_url=inputs["upstream_run_url"], backend_sha=inputs["upstream_sha"])
    identifier(payload["backend_run_id"])
    identifier(payload["backend_run_attempt"])
    matches(payload["backend_sha"], r"[0-9a-f]{40}", "Invalid Backend SHA")
    matches(payload["spec_sha"], r"[0-9a-f]{64}", "Invalid raw schema hash")
    matches(payload["spec_version"], r"[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}", "Invalid API version")
    require(payload["artifact_name"] == ARTIFACT, "Unexpected artifact name")
    require(payload["backend_run_url"].lower() ==
            f"https://github.com/{BACKEND.lower()}/actions/runs/{payload['backend_run_id']}",
            "Unexpected Backend run URL")
    consumer = {
        "cli_repository": CLI,
        "cli_repository_id": repo["id"],
        "cli_ref": f"refs/heads/{branch}",
        "cli_sha": sha,
        "cli_run_id": run_id,
        "cli_run_attempt": attempt,
        "cli_event": "workflow_dispatch",
        "dispatch_event": EVENT,
    }
    return payload, consumer


def verify_run(run, payload, repo, workflow_id):
    require(positive_id(run.get("id")) == int(payload["backend_run_id"])
            and positive_id(run.get("run_attempt")) == int(payload["backend_run_attempt"]),
            "Backend run attempt changed")
    require(run.get("head_sha") == payload["backend_sha"], "Backend source SHA mismatch")
    require(run.get("status") == "completed" and run.get("conclusion") == "success",
            "Backend pipeline must be completed and successful")
    require(run.get("event") == "push" and run.get("head_branch") == payload["backend_branch"],
            "Only matching Backend branch pushes qualify")
    # GitHub run names can reflect the branch-qualified display name, unlike the
    # registered workflow name checked by verify(). Never use display_title as identity.
    expected_names = (WORKFLOW_NAME, f"{WORKFLOW_NAME} ({payload['backend_branch']})")
    require(run.get("path") == WORKFLOW and run.get("name") in expected_names
            and positive_id(run.get("workflow_id")) == workflow_id, "Unexpected Backend workflow")
    repository(run.get("repository"), BACKEND, repo["id"])
    repository(run.get("head_repository"), BACKEND, repo["id"])


def artifact_metadata(value, payload, repo_id):
    value = object_value(value)
    artifact_id = positive_id(value.get("id"))
    require(value.get("name") == ARTIFACT and value.get("expired") is False,
            "Unexpected or expired schema artifact")
    size = value.get("size_in_bytes")
    require(type(size) is int and 0 < size <= ARCHIVE_LIMIT, "Invalid artifact archive size")
    digest = matches(value.get("digest"), r"sha256:[0-9a-f]{64}", "Missing or invalid artifact digest")
    origin = object_value(value.get("workflow_run"))
    require(positive_id(origin.get("id")) == int(payload["backend_run_id"])
            and origin.get("head_sha") == payload["backend_sha"]
            and origin.get("head_branch") == payload["backend_branch"]
            and positive_id(origin.get("repository_id")) == repo_id
            and positive_id(origin.get("head_repository_id")) == repo_id,
            "Artifact run provenance mismatch")
    metadata = {"artifact_id": artifact_id, "artifact_name": ARTIFACT,
                "artifact_digest": digest, "artifact_size_in_bytes": size}
    for field in ("created_at", "updated_at", "expires_at"):
        metadata[f"artifact_{field}"] = matches(
            value.get(field), r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?Z",
            "Invalid artifact timestamp")
    return metadata


def find_artifact(api, run_path, payload, repo_id):
    found = []
    seen = set()
    total = None
    for page in range(1, MAX_PAGES + 1):
        result = api.json(f"{run_path}/artifacts?per_page={PAGE_SIZE}&page={page}")
        count = result.get("total_count")
        require(type(count) is int and 0 <= count <= PAGE_SIZE * MAX_PAGES,
                "Artifact inventory exceeds pagination limit")
        require(total is None or count == total, "Artifact inventory changed during pagination")
        total = count
        entries = result.get("artifacts")
        require(isinstance(entries, list)
                and len(entries) == min(PAGE_SIZE, total - len(seen)), "Incomplete artifact page")
        for entry in entries:
            entry = object_value(entry)
            artifact_id = positive_id(entry.get("id"))
            require(artifact_id not in seen, "Duplicate artifact in inventory")
            seen.add(artifact_id)
            if entry.get("name") == ARTIFACT:
                found.append(artifact_metadata(entry, payload, repo_id))
        if len(seen) == total:
            break
    require(len(found) == 1, "Expected exactly one openapi-spec artifact")
    return found[0]


def safe_schema(raw, payload):
    require(hashlib.sha256(raw).hexdigest() == payload["spec_sha"], "Raw schema hash mismatch")
    schema = object_value(parse_json(raw))
    require(isinstance(schema.get("openapi"), str)
            and re.fullmatch(r"3\.[0-9]+\.[0-9]+", schema["openapi"])
            and isinstance(schema.get("paths"), dict), "Invalid OpenAPI schema")
    info = object_value(schema.get("info"))
    require(info.get("version") == payload["spec_version"], "API version mismatch")
    # This is a safety visitor, not an OpenAPI validator. Map names and literal
    # data are not keywords; map values return to their object's keyword context.
    node_maps = {key: "map:node" for key in ("headers", "content", "encoding", "variables", "webhooks")}
    node_maps.update(links="map:link", callbacks="map:node", responses="responses")
    component_maps = {key: "map:node" for key in (
        "responses", "parameters", "requestBodies", "headers", "securitySchemes", "pathItems", "callbacks",
    )}
    component_maps.update(schemas="map:schema", examples="map:example", links="map:link")

    def child_context(kind, key):
        if kind == "data":
            return "data"
        if kind.startswith("map:"):
            return kind[4:]
        if kind == "schema":
            if key in {"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}:
                return "map:schema"
            if key in {"example", "examples", "default", "enum", "const", "dependentRequired"}:
                return "data"
            return "discriminator" if key == "discriminator" else "schema"
        if kind == "components":
            return component_maps.get(key, "node")
        if kind == "responses":
            return "node"
        if kind == "example" and key == "value":
            return "data"
        if kind == "link" and key in {"parameters", "requestBody"}:
            return "data"
        if key in {"example", "default", "enum", "security", "scopes", "mapping"}:
            return "data"
        if key == "examples":
            return "map:example"
        if key in {"schema", "components"}:
            return key
        return node_maps.get(key, "node")

    def reference_target(reference):
        target = schema
        try:
            for token in unquote(reference[2:]).split("/") if reference != "#" else ():
                token = token.replace("~1", "/").replace("~0", "~")
                target = target[int(token)] if isinstance(target, list) else target[token]
        except (KeyError, IndexError, TypeError, ValueError):
            raise VerificationError("Unresolved local schema reference") from None
        return target

    pending = [(schema, 0, "node")]
    referenced = set()
    nodes = 0
    while pending:
        value, depth, kind = pending.pop()
        nodes += 1
        require(depth <= 64 and nodes <= 250000, "Schema complexity exceeds limit")
        if isinstance(value, (dict, list)):
            require(nodes + len(pending) + len(value) <= 250000, "Schema complexity exceeds limit")
        if kind == "discriminator":
            discriminator = object_value(value)
            # oapi-codegen v2.6.0 union.tmpl interpolates mapping keys verbatim;
            # schema.go's Discriminator.JSONTag also embeds propertyName unescaped.
            matches(discriminator.get("propertyName"), r"[A-Za-z0-9_.:-]{1,128}",
                    "Unsafe discriminator property name")
            for literal, target in object_value(discriminator.get("mapping", {})).items():
                matches(literal, r"[A-Za-z0-9_.:-]{1,128}", "Unsafe discriminator mapping literal")
                require(isinstance(target, str), "Invalid discriminator mapping target")
        if isinstance(value, dict):
            if kind == "schema":
                # generateUnion falls back to RefPathToObjName for unmapped
                # variants, including partial mappings. Bound all union refs so
                # later schema merging cannot introduce an unsafe implicit label.
                for union in ("oneOf", "anyOf"):
                    elements = value.get(union, [])
                    require(isinstance(elements, list), "Invalid union schema")
                    for element in elements:
                        if isinstance(element, dict) and "$ref" in element:
                            reference = element["$ref"]
                            require(isinstance(reference, str), "Invalid union schema reference")
                            matches(reference.rsplit("/", 1)[-1], r"[A-Za-z0-9_.:-]{1,128}",
                                    "Unsafe implicit discriminator literal")
            keywords = kind != "data" and not kind.startswith("map:")
            for key, child in value.items():
                if keywords:
                    require(not key.lower().startswith("x-"), "Schema vendor extensions are not allowed")
                    require(key not in {"$id", "$schema", "$dynamicRef", "$recursiveRef"},
                            "Schema-directed resolution is not allowed")
                    if key in {"$ref", "operationRef"}:
                        require(isinstance(child, str) and (child == "#" or child.startswith("#/")),
                                "External or invalid schema reference")
                        target = reference_target(child)
                        target_kind = "node" if key == "operationRef" else kind
                        identity = (id(target), target_kind)
                        if identity not in referenced:
                            # Referenced example data becomes codegen input. Visit it
                            # in the reference's context too, without looping on cycles.
                            referenced.add(identity)
                            pending.append((target, depth + 1, target_kind))
                pending.append((child, depth + 1, child_context(kind, key)))
        elif isinstance(value, list):
            pending.extend((child, depth + 1, kind) for child in value)


def unpack(archive):
    require(0 < len(archive) <= ARCHIVE_LIMIT, "Downloaded archive exceeds size limit")
    # Bound central-directory allocation before ZipFile constructs member objects.
    # Two small files do not need multidisk/ZIP64 archives; reject those formats.
    end = archive.rfind(b"PK\x05\x06", max(0, len(archive) - 65557))
    require(end >= 0 and end + 22 <= len(archive), "Invalid ZIP end record")
    _, disk, directory_disk, disk_count, count, size, offset, comment_size = struct.unpack_from(
        "<4s4H2IH", archive, end)
    require(disk == directory_disk == 0 and disk_count == count == 2
            and size <= 65536 and offset + size == end
            and end + 22 + comment_size == len(archive), "Invalid or oversized ZIP directory")
    try:
        with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
            require(sorted(bundle.namelist()) == ["openapi-manifest.json", "openapi.json"],
                    "Unexpected or duplicate archive members")
            contents = {}
            for name, limit in (("openapi.json", SCHEMA_LIMIT), ("openapi-manifest.json", MANIFEST_LIMIT)):
                member = bundle.getinfo(name)
                mode = member.external_attr >> 16
                require(stat.S_IFMT(mode) in (0, stat.S_IFREG)
                        and member.orig_filename == name
                        and not member.is_dir() and not (member.flag_bits & 1)
                        and member.compress_type in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED),
                        "Unsafe ZIP member type or encoding")
                require(0 < member.file_size <= limit and member.compress_size <= ARCHIVE_LIMIT,
                        "Uncompressed artifact exceeds size limit")
                with bundle.open(member) as stream:
                    raw = stream.read(limit + 1)
                require(len(raw) == member.file_size and len(raw) <= limit, "ZIP member size mismatch")
                contents[name] = raw
            return contents["openapi.json"], contents["openapi-manifest.json"]
    except (zipfile.BadZipFile, NotImplementedError, RuntimeError, EOFError, ValueError, zlib.error):
        raise VerificationError("Invalid schema ZIP archive") from None


def verify(api, payload, consumer):
    build_identity(payload.get("backend_branch"), consumer.get("cli_run_id"), consumer.get("cli_run_attempt"))
    require(consumer.get("cli_event") == "workflow_dispatch" and consumer.get("dispatch_event") == EVENT
            and consumer.get("cli_ref") == f"refs/heads/{payload['backend_branch']}",
            "Consumer lineage must match the Backend branch")
    prefix = f"repos/{BACKEND}"
    # Repo IDs and workflow identity come from GitHub, not the dispatch assertions.
    repo = repository(api.json(prefix), BACKEND)
    workflow = api.json(f"{prefix}/actions/workflows/pipeline.yml")
    workflow_id = positive_id(workflow.get("id"))
    require(workflow.get("path") == WORKFLOW and workflow.get("name") == WORKFLOW_NAME,
            "Unexpected registered Backend workflow")
    run_path = f"{prefix}/actions/runs/{payload['backend_run_id']}"
    verify_run(api.json(run_path), payload, repo, workflow_id)
    metadata = find_artifact(api, run_path, payload, repo["id"])
    artifact_path = f"{prefix}/actions/artifacts/{metadata['artifact_id']}"

    def check_artifact():
        same_json(artifact_metadata(api.json(artifact_path), payload, repo["id"]), metadata,
                  "Artifact metadata changed")

    check_artifact()
    archive = api.get(f"{artifact_path}/zip", ARCHIVE_LIMIT)
    archive_hash = hashlib.sha256(archive).hexdigest()
    require(len(archive) == metadata["artifact_size_in_bytes"]
            and f"sha256:{archive_hash}" == metadata["artifact_digest"], "Artifact archive hash or size mismatch")
    raw, manifest_raw = unpack(archive)
    safe_schema(raw, payload)
    manifest = object_value(parse_json(manifest_raw))
    expected = {
        "schema_version": 1,
        "backend_repository": repo["full_name"],
        "backend_workflow": WORKFLOW,
        "backend_event": "push",
        **{key: value for key, value in payload.items() if key != "backend_run_url"},
    }
    same_json(manifest, expected, "Producer manifest does not match the run attempt and schema")
    check_artifact()
    verify_run(api.json(run_path), payload, repo, workflow_id)
    provenance = {
        **expected,
        **consumer,
        **metadata,
        "backend_repository_id": repo["id"],
        "backend_workflow_id": workflow_id,
        "backend_run_url": f"https://github.com/{repo['full_name']}/actions/runs/{payload['backend_run_id']}",
        "archive_sha256": archive_hash,
        "manifest_sha256": hashlib.sha256(manifest_raw).hexdigest(),
    }
    return raw, manifest_raw, provenance


def fetch(output, api, payload, consumer):
    require(not os.path.lexists(output), "Output directory already exists; choose a new directory")
    require(output.parent.is_dir(), "Output parent directory must exist")
    raw, manifest, provenance = verify(api, payload, consumer)
    # Stage only verified bytes, then publish the set together. Never overwrite
    # an existing output or leave a half-written evidence set after I/O failure.
    with tempfile.TemporaryDirectory(prefix=".unstable-schema-", dir=output.parent) as staging:
        stage = Path(staging)
        (stage / "openapi.json").write_bytes(raw)
        (stage / "manifest.json").write_bytes(manifest)
        (stage / "provenance.json").write_bytes(encoded(provenance))
        require(not os.path.lexists(output), "Output directory appeared during verification")
        os.rename(stage, output)
    return provenance


def recheck(directory, api, payload, consumer):
    require(not directory.is_symlink() and directory.is_dir(), "Input must be a regular directory")
    require({entry.name for entry in directory.iterdir()} == FILES, "Unexpected evidence directory members")
    local_raw = read_bounded(directory / "openapi.json", SCHEMA_LIMIT)
    local_manifest = read_bounded(directory / "manifest.json", MANIFEST_LIMIT)
    local_provenance = object_value(parse_json(read_bounded(directory / "provenance.json", MANIFEST_LIMIT)))
    raw, manifest, provenance = verify(api, payload, consumer)
    require(local_raw == raw and local_manifest == manifest, "Saved schema or manifest changed")
    same_json(local_provenance, provenance, "Saved provenance no longer matches verified inputs")
    return provenance


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("fetch", help="Verify and save a new schema evidence directory").add_argument(
        "--output", type=Path, required=True)
    commands.add_parser("recheck", help="Reverify saved evidence before publication (no writes)").add_argument(
        "--input", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        api = GitHub()
        payload, consumer = context(os.environ)
        if args.command == "fetch":
            provenance = fetch(args.output, api, payload, consumer)
        else:
            provenance = recheck(args.input, api, payload, consumer)
        sys.stdout.write(encoded(provenance).decode("utf-8"))
        return 0
    except VerificationError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    except (OSError, ValueError, RecursionError):
        print("ERROR: Could not read or write bounded verification inputs", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("ERROR: Verification interrupted", file=sys.stderr)
        return 130


if __name__ == "__main__":
    sys.exit(main())
