#!/usr/bin/env python3
"""Validate sidecar manifests and assemble a deterministic unpublished catalog."""

import argparse
import hashlib
import json
import pathlib


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--models-lock", default="speech/sidecars/kokoro/models.lock.json")
    args = parser.parse_args()
    directory = pathlib.Path(args.directory)
    entries = []
    for manifest_path in sorted(directory.glob("*.manifest.json")):
        entry = json.loads(manifest_path.read_text(encoding="utf-8"))
        package = directory / entry["file"]
        if package.stat().st_size != entry["size"]:
            raise SystemExit(f"size mismatch: {package}")
        if hashlib.sha256(package.read_bytes()).hexdigest() != entry["sha256"]:
            raise SystemExit(f"checksum mismatch: {package}")
        entries.append(entry)
    expected = {"darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
    actual = {entry["target"] for entry in entries}
    if actual != expected:
        raise SystemExit(f"target mismatch: missing={sorted(expected-actual)} extra={sorted(actual-expected)}")
    models = json.loads(pathlib.Path(args.models_lock).read_text(encoding="utf-8"))
    releases = []
    for entry in entries:
        goos, goarch = entry["target"].split("-", 1)
        model = models[entry["backend"]]
        model_version = entry["backend"] + "-" + model["revision"]
        model_artifacts = []
        for index, locked_file in enumerate(model["files"]):
            model_artifacts.append({
                "name": ("model-config", "model-weights", "model-voice")[index],
                "target": locked_file["path"],
                "url": "https://huggingface.co/" + model["repository"] + "/resolve/" + model["revision"] + "/" + locked_file["path"],
                "sha256": locked_file["sha256"], "size": locked_file["size"],
                "license": "Apache-2.0", "kind": "model",
            })
        releases.append({
            "engine": "kokoro", "version": entry["version"], "goos": goos,
            "goarch": goarch, "backend": entry["backend"], "voice": entry["voice"],
            "model_version": model_version,
            "artifacts": [{
                "name": entry["artifact_name"], "url": args.base_url.rstrip("/") + "/" + entry["file"],
                "sha256": entry["sha256"], "size": entry["size"],
                "license": "Apache-2.0 AND third-party-notices", "kind": "runtime", "executable": True,
            }] + model_artifacts,
        })
    pathlib.Path(args.output).write_text(json.dumps(releases, indent=2, sort_keys=True) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
