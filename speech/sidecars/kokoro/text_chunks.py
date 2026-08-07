"""Text preparation shared by mneme's Kokoro launchers."""

import re
import textwrap


MAX_CHUNK_CHARS = 400
_SENTENCE_BOUNDARY = re.compile(r"(?<=[.!?…;:])\s+")


def prepare_text(text: str, max_chars: int = MAX_CHUNK_CHARS) -> str:
    """Insert model chunk boundaries while preserving spoken punctuation."""
    paragraphs = [" ".join(line.split()) for line in text.splitlines() if line.strip()]
    chunks: list[str] = []
    for paragraph in paragraphs:
        for sentence in _SENTENCE_BOUNDARY.split(paragraph):
            chunks.extend(
                textwrap.wrap(
                    sentence,
                    width=max_chars,
                    break_long_words=False,
                    break_on_hyphens=False,
                )
            )
    return "\n".join(chunks)
