#!/usr/bin/env python3
"""Build and package verified schema inputs offline, from the CLI repository root.

Run after trusted CI matched-branch checkout, schema processing/pinned v2.6.0 generation,
verifier checks, and Go tests. This is not a GitHub verifier, generator, test
runner, or publisher. HEAD identifies the trusted base; current Go inputs and
schemas identify the intentionally generated workspace (SourceDirty=true).

--evidence contains the verifier's openapi.json, manifest.json, provenance.json.
--output must not exist and its parent must exist. A staged package is exposed
only after build and isolated version/help checks succeed. No archive is made.
Actions artifact downloads may lose the executable bit; after download run:

    sha256sum -c SHA256SUMS
    chmod +x jetscale-unstable

For matched Backend/CLI main builds, the binary is jetscale (chmod +x jetscale).
Only main -> live/jetscale and unstable -> unstable/jetscale-unstable are allowed.
Versions are 0.0.0-main.RUN.ATTEMPT or 0.0.0-unstable.RUN.ATTEMPT. Main packages
are live-channel Actions artifacts, not stable releases; this does not publish.

Success prints enriched provenance JSON only. Failures print static diagnostics
without child output on stderr and exit 1 (usage: 2, interruption: 130). No retries;
build timeout is 120 seconds, each other child timeout is 10 seconds. Go network
fetches are disabled; version/help run with an empty home and no inherited auth.
These smoke checks do not prove deployed API compatibility or sandbox arbitrary
code: CI must admit only trusted source and generator configuration.
"""

import argparse
from datetime import datetime, timezone
import hashlib
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile

# Reuse only the verifier's stdlib parsing/file guards, never its online client.
from unstable_build import (
    ARTIFACT, BACKEND, CLI, EVENT, FILES, MANIFEST_LIMIT, SCHEMA_LIMIT, WORKFLOW,
    VerificationError, build_identity, encoded, identifier, matches, object_value, parse_json,
    read_bounded, require, same_json,
)

MODULE = "github.com/Jetscale-ai/cli"
GENERATED = "internal/api/generated/client.gen.go"
SOURCE_LIMIT = 128 * 1024 * 1024
SOURCE_FILE_LIMIT = 32 * 1024 * 1024
CHILD_OUTPUT_LIMIT = 2 * 1024 * 1024


def sha(data):
    return hashlib.sha256(data).hexdigest()


def run(args, env, label, timeout=10, capture=True):
    try:
        result = subprocess.run(
            args, env=env, stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
            stderr=subprocess.DEVNULL, timeout=timeout, check=True,
        )
    except subprocess.TimeoutExpired:
        raise VerificationError(f"{label} timed out; no package produced") from None
    except (OSError, subprocess.CalledProcessError):
        raise VerificationError(f"{label} failed; no package produced") from None
    data = result.stdout if capture else b""
    require(len(data) <= CHILD_OUTPUT_LIMIT, "Child output exceeds size limit")
    return data


def environment(home):
    # Allowlist instead of subtracting known token names. Do not inherit GOFLAGS,
    # GOENV, GOWORK, Git config, proxy settings, loader hooks, or auth variables.
    original_home = Path.home()
    gopath = os.environ.get("GOPATH", str(original_home / "go"))
    return {
        "PATH": os.environ.get("PATH", os.defpath),
        "HOME": str(home), "XDG_CONFIG_HOME": str(home / "config"),
        "XDG_CACHE_HOME": str(home / "cache"), "TMPDIR": str(home),
        "GOPATH": gopath,
        "GOMODCACHE": os.environ.get("GOMODCACHE", str(Path(gopath.split(os.pathsep)[0]) / "pkg/mod")),
        "GOCACHE": os.environ.get("GOCACHE", str(original_home / ".cache/go-build")),
        "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local",
        "GOENV": "off", "GOWORK": "off", "GOFLAGS": "",
        "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1",
        "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_TERMINAL_PROMPT": "0", "LC_ALL": "C", "TZ": "UTC",
        "NO_COLOR": "1", "TERM": "dumb",
    }


def regular_path(path):
    require(not any(part.is_symlink() for part in (path, *path.parents)),
            "Symlink paths are not allowed")


def workspace_read(name, limit):
    path = Path(name)
    require(not path.is_absolute() and ".." not in path.parts,
            "Source path must remain inside the checkout")
    regular_path(path)
    return read_bounded(path, limit)


def source_hash(env):
    paths = run([
        "git", "--no-pager", "--no-optional-locks", "ls-files", "-z",
        "--cached", "--others", "--exclude-standard", "--", "*.go", "go.mod", "go.sum",
    ], env, "Go source enumeration").split(b"\0")
    require(len(paths) <= 10000, "Too many Go source inputs")
    digest = hashlib.sha256()
    total = 0
    # Go strings and len(path) are byte based, including for non-ASCII filenames.
    for path in sorted(set(paths) - {b""}):
        try:
            data = workspace_read(os.fsdecode(path), SOURCE_FILE_LIMIT)
        except FileNotFoundError:
            continue  # Same tracked-deletion semantics as mage localSourceHash.
        total += len(data)
        require(total <= SOURCE_LIMIT, "Go source inputs exceed size limit")
        digest.update(str(len(path)).encode() + b":" + path + str(len(data)).encode() + b":")
        digest.update(data)
    return digest.hexdigest()


def api_version(raw):
    spec = object_value(parse_json(raw))
    version = object_value(spec.get("info")).get("version")
    return matches(version, r"[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}", "Invalid API version")


def inputs(evidence, env):
    regular_path(evidence)
    require(evidence.is_dir() and {p.name for p in evidence.iterdir()} == FILES,
            "Expected the verifier's three-file evidence directory")
    raw = read_bounded(evidence / "openapi.json", SCHEMA_LIMIT)
    manifest_raw = read_bounded(evidence / "manifest.json", MANIFEST_LIMIT)
    provenance = object_value(parse_json(read_bounded(evidence / "provenance.json", MANIFEST_LIMIT)))
    manifest = object_value(parse_json(manifest_raw))
    for key in ("backend_sha", "cli_sha"):
        matches(provenance.get(key), r"[0-9a-f]{40}", "Invalid source SHA")
    for key in ("backend_run_id", "backend_run_attempt", "cli_run_id", "cli_run_attempt"):
        identifier(provenance.get(key))
    branch = provenance.get("backend_branch")
    build_identity(branch, provenance["cli_run_id"], provenance["cli_run_attempt"])
    version = api_version(raw)
    expected_manifest = {
        "schema_version": 1, "backend_repository": provenance.get("backend_repository"),
        "backend_workflow": WORKFLOW, "backend_event": "push", "backend_branch": branch,
        "backend_sha": provenance["backend_sha"], "backend_run_id": provenance["backend_run_id"],
        "backend_run_attempt": provenance["backend_run_attempt"], "spec_sha": sha(raw),
        "spec_version": version, "artifact_name": ARTIFACT,
    }
    require(isinstance(expected_manifest["backend_repository"], str)
            and expected_manifest["backend_repository"].lower() == BACKEND.lower(),
            "Unexpected Backend repository")
    same_json(manifest, expected_manifest, "Raw schema and producer manifest disagree")
    same_json({key: provenance.get(key) for key in manifest}, manifest,
              "Manifest and verifier provenance disagree")
    require(provenance.get("manifest_sha256") == sha(manifest_raw), "Manifest hash mismatch")
    expected_consumer = {
        "cli_repository": CLI, "cli_ref": f"refs/heads/{branch}",
        "cli_event": "workflow_dispatch", "dispatch_event": EVENT,
    }
    require(all(provenance.get(k) == v for k, v in expected_consumer.items()),
            "Expected verified matched-branch CLI receiver provenance")
    require(os.environ.get("GITHUB_REPOSITORY", "").lower() == CLI.lower()
            and os.environ.get("GITHUB_REF") == expected_consumer["cli_ref"]
            and os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch",
            "Packaging requires the trusted matched-branch CLI CI receiver")
    for variable, key in (("GITHUB_SHA", "cli_sha"), ("GITHUB_RUN_ID", "cli_run_id"),
                          ("GITHUB_RUN_ATTEMPT", "cli_run_attempt")):
        require(os.environ.get(variable) == provenance[key], "CLI CI run does not match evidence")
    head = run(["git", "--no-pager", "--no-optional-locks", "rev-parse", "HEAD"], env, "Git HEAD check")
    require(head.strip() == provenance["cli_sha"].encode(), "Git HEAD does not match CLI provenance")
    processed = workspace_read("openapi/spec.json", SCHEMA_LIMIT)
    require(api_version(processed) == version, "Processed API version differs from raw schema/provenance")
    generated = workspace_read(GENERATED, SOURCE_FILE_LIMIT)
    require(bool(generated), "Generated client is empty")
    hashes = {
        "raw_schema_sha256": sha(raw), "processed_schema_sha256": sha(processed),
        "source_sha256": source_hash(env), "generated_client_sha256": sha(generated),
        "go_mod_sha256": sha(workspace_read("go.mod", SOURCE_FILE_LIMIT)),
        "go_sum_sha256": sha(workspace_read("go.sum", SOURCE_FILE_LIMIT)),
    }
    return raw, processed, manifest_raw, provenance, hashes


def package(evidence, output):
    require(sys.platform == "linux" and platform.machine() in ("x86_64", "amd64"),
            "Packaging requires a native Linux amd64 runner")
    regular_path(output)
    require(not os.path.lexists(output), "Output directory already exists; choose a new directory")
    require(output.parent.is_dir(), "Output parent directory must exist")
    # Keep isolated homes outside the published package. Only the stage is renamed.
    with tempfile.TemporaryDirectory(prefix=".unstable-package-", dir=output.parent) as temporary:
        work = Path(temporary).absolute()
        home = work / "home"
        home.mkdir()
        env = environment(home)
        before = inputs(evidence, env)
        raw, processed, manifest_raw, provenance, hashes = before
        go_version = run(["go", "version"], env, "Go version check").decode("ascii").strip()
        matches(go_version, r"go version go[0-9]+\.[0-9]+(?:\.[0-9]+)? linux/amd64",
                "Expected stable native Linux amd64 Go toolchain")
        channel, binary_name, version = build_identity(
            provenance["backend_branch"], provenance["cli_run_id"], provenance["cli_run_attempt"],
        )
        date = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        expected = {
            "binary": binary_name, "version": version, "commit": provenance["cli_sha"],
            "date": date, "channel": channel, "backend_sha": provenance["backend_sha"],
            "schema_sha256": hashes["processed_schema_sha256"], "api_version": provenance["spec_version"],
            "source_sha256": hashes["source_sha256"], "source_dirty": "true",
        }
        fields = {
            "cmd.version": version, "cmd.commit": expected["commit"], "cmd.date": date,
            "buildinfo.Channel": channel, "buildinfo.BackendSHA": expected["backend_sha"],
            "buildinfo.SchemaSHA256": expected["schema_sha256"], "buildinfo.APIVersion": expected["api_version"],
            "buildinfo.SourceSHA256": expected["source_sha256"], "buildinfo.SourceDirty": "true",
        }
        flags = "-s -w " + " ".join(f"-X '{MODULE}/internal/{key}={value}'" for key, value in fields.items())
        stage = work / "stage"
        stage.mkdir()
        binary = stage / binary_name
        run(["go", "build", "-mod=readonly", "-trimpath", "-ldflags", flags,
             "-o", str(binary), "./cmd/jetscale"], env, "Go build", timeout=120, capture=False)
        binary_raw = read_bounded(binary, SOURCE_LIMIT)
        require(bool(binary_raw), "Build produced an empty binary")
        binary.chmod(0o755)
        smoke_home = work / "smoke-home"
        smoke_home.mkdir()
        smoke_env = environment(smoke_home)
        actual = object_value(parse_json(run([str(binary), "version", "-o", "json"], smoke_env,
                                             "Binary version check")))
        same_json(actual, expected, "Binary version metadata does not match packaged inputs")
        help_output = run([str(binary), "--help"], smoke_env, "Binary help check")
        require(b"Usage:" in help_output and binary_name.encode() in help_output,
                "Binary help output is invalid")
        require(inputs(evidence, env) == before, "Build inputs changed during packaging")
        require(read_bounded(binary, SOURCE_LIMIT) == binary_raw, "Binary changed during smoke checks")
        enriched = {
            **provenance, **hashes, "generator_version": "v2.6.0", "binary_sha256": sha(binary_raw),
            "go_version": go_version, "platform": "linux/amd64", "binary_version": version,
            "build_date": date, "channel": channel, "source_dirty": True,
        }
        (stage / "raw-schema.json").write_bytes(raw)
        (stage / "processed-schema.json").write_bytes(processed)
        (stage / "manifest.json").write_bytes(manifest_raw)
        (stage / "provenance.json").write_bytes(encoded(enriched))
        sums = "".join(f"{sha(read_bounded(path, SOURCE_LIMIT))}  {path.name}\n"
                       for path in sorted(stage.iterdir()))
        (stage / "SHA256SUMS").write_text(sums, encoding="ascii")
        require(not os.path.lexists(output), "Output directory appeared during packaging")
        os.rename(stage, output)
    return enriched


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        result = package(args.evidence, args.output)
        sys.stdout.write(encoded(result).decode("utf-8"))
        return 0
    except VerificationError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    except (OSError, ValueError, RecursionError):
        print("ERROR: Could not read or write bounded package inputs", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("ERROR: Packaging interrupted", file=sys.stderr)
        return 130


if __name__ == "__main__":
    sys.exit(main())
