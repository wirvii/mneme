#!/usr/bin/env python3
"""Run a packaged Kokoro healthcheck against verified immutable model files."""

import argparse
import hashlib
import json
import os
import pathlib
import subprocess
import tempfile
import urllib.request


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact-dir", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--backend", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--models-lock", default="speech/sidecars/kokoro/models.lock.json")
    args = parser.parse_args()

    suffix = ".exe" if args.target.startswith("windows-") else ""
    artifact = pathlib.Path(args.artifact_dir) / f"mneme-kokoro-{args.version}-{args.target}{suffix}"
    if not artifact.is_file():
        raise SystemExit(f"sidecar not found: {artifact}")
    artifact.chmod(0o700)

    lock = json.loads(pathlib.Path(args.models_lock).read_text(encoding="utf-8"))[args.backend]
    with tempfile.TemporaryDirectory(prefix="mneme-kokoro-smoke-") as temporary:
        model_dir = pathlib.Path(temporary) / "model"
        for locked_file in lock["files"]:
            destination = model_dir / locked_file["path"]
            destination.parent.mkdir(parents=True, exist_ok=True)
            url = "https://huggingface.co/{}/resolve/{}/{}".format(
                lock["repository"], lock["revision"], locked_file["path"]
            )
            request = urllib.request.Request(url, headers={"User-Agent": "mneme-sidecar-smoke"})
            digest = hashlib.sha256()
            size = 0
            with urllib.request.urlopen(request, timeout=60) as response, destination.open("wb") as output:
                while chunk := response.read(1024 * 1024):
                    output.write(chunk)
                    digest.update(chunk)
                    size += len(chunk)
            if size != locked_file["size"] or digest.hexdigest() != locked_file["sha256"]:
                raise SystemExit(f"model verification failed: {locked_file['path']}")

        environment = dict(os.environ)
        environment.update({
            "HF_HUB_OFFLINE": "1",
            "TRANSFORMERS_OFFLINE": "1",
            "NO_PROXY": "*",
            "MNEME_SPEECH_DIAGNOSTIC": "1",
        })
        request = json.dumps({"action": "healthcheck", "voice": "ef_dora", "model": str(model_dir)}) + "\n"
        completed = subprocess.run(
            [str(artifact.resolve())], input=request, text=True, capture_output=True,
            env=environment, timeout=300, check=False,
        )
        if completed.returncode != 0 or '"ok": true' not in completed.stdout.lower():
            raise SystemExit(
                f"sidecar healthcheck failed ({completed.returncode}): {completed.stderr[-2000:]}"
            )


if __name__ == "__main__":
    main()
