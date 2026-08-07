#!/usr/bin/env python3
"""PyTorch CPU implementation of mneme's stdin-only Kokoro contract."""

import json
import os
import sys
import traceback

from text_chunks import prepare_text


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
        import sounddevice
        from kokoro import KPipeline
    except Exception as error:
        fail("runtime_import_failed", error)
    if request.get("action") == "healthcheck":
        model = request.get("model", "")
        if not model:
            fail("invalid_request")
        try:
            pipeline = KPipeline(lang_code="e", repo_id=model)
            next(iter(pipeline("Prueba.", voice=request.get("voice", "ef_dora"))))
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
        pipeline = KPipeline(lang_code=lang_code, repo_id=model)
        for _, _, audio in pipeline(prepare_text(text), voice=request.get("voice", "ef_dora"), speed=float(request.get("rate", 1.0))):
            sounddevice.play(audio, 24000, blocking=True)
    except Exception as error:
        fail("synthesis_failed", error)
    print(json.dumps({"ok": True, "engine": "kokoro"}))


if __name__ == "__main__":
    main()
