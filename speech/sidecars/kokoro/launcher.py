#!/usr/bin/env python3
"""Stable stdin-only launcher contract for mneme's managed Kokoro bundle."""

import json
import os
from pathlib import Path
import sys
import tempfile
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
    action = request.get("action", "speak")
    try:
        from misaki import en as _misaki_en  # noqa: F401
        from mlx_audio.tts.generate import generate_audio
        from mlx_audio.tts.utils import load_model
    except Exception as error:
        fail("runtime_import_failed", error)
    model_path = request.get("model", "")
    if not model_path:
        fail("invalid_request")
    try:
        loaded_model = load_model(
            Path(model_path), model_type="kokoro", model_name_parts=["kokoro"]
        )
    except Exception as error:
        fail("model_load_failed", error)

    def synthesize(text: str, voice: str, lang_code: str, speed: float) -> Path:
        output_dir = Path(tempfile.mkdtemp(prefix="mneme-kokoro-"))
        output_file = output_dir / "speech.wav"
        voice_file = Path(model_path) / "voices" / f"{voice}.safetensors"
        if not voice_file.is_file():
            fail("voice_not_found")
        generate_audio(
            text=prepare_text(text),
            model=loaded_model,
            voice=str(voice_file),
            speed=speed,
            lang_code=lang_code,
            output_path=str(output_dir),
            file_prefix="speech",
            audio_format="wav",
            join_audio=True,
            play=False,
            verbose=False,
        )
        if not output_file.is_file() or output_file.stat().st_size == 0:
            fail("synthesis_failed")
        return output_file

    if action == "healthcheck":
        try:
            output_file = synthesize(
                "Prueba.", request.get("voice", "ef_dora"), "e", 1.0
            )
            output_file.unlink(missing_ok=True)
            output_file.parent.rmdir()
        except Exception as error:
            fail("model_healthcheck_failed", error)
        print(json.dumps({"ok": True, "backend": "mlx"}))
        return
    text = request.get("text", "")
    voice = request.get("voice", "ef_dora")
    if not text:
        fail("invalid_request")
    language = request.get("language", "es").lower().split("-")[0]
    lang_code = {"es": "e", "en": "a", "fr": "f", "it": "i", "pt": "p"}.get(language, "e")
    try:
        import sounddevice
        from scipy.io import wavfile

        output_file = synthesize(
            text, voice, lang_code, float(request.get("rate", 1.0))
        )
        sample_rate, audio = wavfile.read(output_file)
        sounddevice.play(audio, sample_rate, blocking=True)
        output_file.unlink(missing_ok=True)
        output_file.parent.rmdir()
    except Exception as error:
        fail("synthesis_failed", error)
    print(json.dumps({"ok": True, "engine": "kokoro", "voice": voice}))


if __name__ == "__main__":
    main()
