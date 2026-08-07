#!/usr/bin/env python3
"""Build one self-contained Kokoro launcher and its verifiable manifest."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import pathlib
import shutil
import subprocess
import sys


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", required=True)
    parser.add_argument("--backend", choices=("mlx", "pytorch-cpu"), required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", default="dist/speech")
    args = parser.parse_args()

    root = pathlib.Path(__file__).resolve().parents[1]
    source = root / "speech" / "sidecars" / "kokoro"
    launcher = source / ("launcher.py" if args.backend == "mlx" else "launcher_pytorch.py")
    requirements = source / ("requirements-darwin-arm64.lock" if args.backend == "mlx" else "requirements-pytorch-cpu.lock")
    subprocess.run([sys.executable, "-m", "pip", "install", "--requirement", str(requirements), "pyinstaller==6.21.0"], check=True)
    work = root / "tmp" / "speech-build" / args.target
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True)
    pyinstaller = [
        sys.executable, "-m", "PyInstaller", "--clean", "--onefile", "--name", "launcher",
        "--distpath", str(work / "bundle"), "--workpath", str(work / "work"),
        "--specpath", str(work),
        "--collect-all", "misaki",
        "--collect-all", "espeakng_loader",
        "--collect-all", "phonemizer",
        "--collect-all", "segments",
        "--collect-all", "csvw",
        "--collect-all", "language_tags",
    ]
    if args.backend == "mlx":
        pyinstaller.extend(
            [
                "--collect-binaries",
                "mlx",
                "--collect-submodules",
                "mlx_audio.tts.models.kokoro",
                "--hidden-import",
                "mlx._reprlib_fix",
            ]
        )
        mlx_spec = importlib.util.find_spec("mlx.core")
        if mlx_spec is None or mlx_spec.origin is None:
            raise SystemExit("mlx package not found after installation")
        metallib = pathlib.Path(mlx_spec.origin).parent / "lib" / "mlx.metallib"
        if not metallib.is_file():
            raise SystemExit("mlx.metallib not found")
        pyinstaller.extend(["--add-data", str(metallib) + ":mlx/lib"])
    pyinstaller.append(str(launcher))
    subprocess.run(pyinstaller, check=True)

    output = root / args.output
    output.mkdir(parents=True, exist_ok=True)
    built_name = "launcher.exe" if args.target.startswith("windows-") else "launcher"
    built = work / "bundle" / built_name
    package = output / f"mneme-kokoro-{args.version}-{args.target}{'.exe' if built_name.endswith('.exe') else ''}"
    shutil.copy2(built, package)
    digest = hashlib.sha256(package.read_bytes()).hexdigest()
    manifest = {
        "engine": "kokoro", "version": args.version, "target": args.target,
        "backend": args.backend, "voice": "ef_dora", "file": package.name,
        "artifact_name": built_name, "size": package.stat().st_size, "sha256": digest,
        "licenses": ["Apache-2.0", "MIT", "model-specific"],
    }
    (output / f"{package.name}.manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(manifest))


if __name__ == "__main__":
    main()
