#!/usr/bin/env python3
"""PyTorch CPU implementation of mneme's stdin-only Kokoro contract."""

import json
import os
import pathlib
import sys
import traceback

from text_chunks import prepare_text


def build_pipeline(model_dir: str, lang_code: str):
    from kokoro import KModel, KPipeline

    root = pathlib.Path(model_dir)
    model = KModel(
        config=str(root / "config.json"),
        model=str(root / "kokoro-v1_0.pth"),
    ).to("cpu").eval()
    return KPipeline(lang_code=lang_code, model=model), root


def fail(message: str, cause: Exception | None = None) -> None:
    print(json.dumps({"ok": False, "error": message}), file=sys.stderr)
    if cause is not None and os.environ.get("MNEME_SPEECH_DIAGNOSTIC") == "1":
        print(type(cause).__name__ + ": " + str(cause), file=sys.stderr)
        traceback.print_exception(cause, file=sys.stderr)
    raise SystemExit(1)


def main() -> None:
    os.environ.setdefault("HF_HUB_OFFLINE", "1")
    os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
    request = json.loads(sys.stdin.readline())
    try:
        import kokoro
    except Exception as error:
        fail("runtime_import_failed", error)
    if request.get("action") == "healthcheck":
        model = request.get("model", "")
        if not model:
            fail("invalid_request")
        try:
            pipeline, root = build_pipeline(model, "e")
            voice = root / "voices" / (request.get("voice", "ef_dora") + ".pt")
            next(iter(pipeline("Prueba.", voice=str(voice))))
        except Exception as error:
            fail("model_healthcheck_failed", error)
        print(json.dumps({"ok": True, "backend": "pytorch-cpu"}))
        return
    text, model = request.get("text", ""), request.get("model", "")
    if not text or not model:
        fail("invalid_request")
    language = request.get("language", "es").lower().split("-")[0]
    lang_code = {"es": "e", "en": "a", "fr": "f", "it": "i", "pt": "p"}.get(language, "e")
    try:
        import sounddevice

        pipeline, root = build_pipeline(model, lang_code)
        voice = root / "voices" / (request.get("voice", "ef_dora") + ".pt")
        for _, _, audio in pipeline(prepare_text(text), voice=str(voice), speed=float(request.get("rate", 1.0))):
            sounddevice.play(audio, 24000, blocking=True)
    except Exception as error:
        fail("synthesis_failed", error)
    print(json.dumps({"ok": True, "engine": "kokoro"}))


if __name__ == "__main__":
    main()
