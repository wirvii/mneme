import unittest

from text_chunks import prepare_text


class PrepareTextTests(unittest.TestCase):
    def test_preserves_punctuation_at_sentence_boundaries(self) -> None:
        self.assertEqual(
            prepare_text("Hola, mundo. ¿Cómo estás? Muy bien; gracias."),
            "Hola, mundo.\n¿Cómo estás?\nMuy bien;\ngracias.",
        )

    def test_normalizes_existing_lines(self) -> None:
        self.assertEqual(prepare_text("  Uno.\n\n Dos.  "), "Uno.\nDos.")

    def test_wraps_only_when_a_sentence_exceeds_the_limit(self) -> None:
        prepared = prepare_text("uno dos tres cuatro", max_chars=8)
        self.assertEqual(prepared, "uno dos\ntres\ncuatro")
        self.assertTrue(all(len(chunk) <= 8 for chunk in prepared.splitlines()))

    def test_does_not_break_a_single_long_word(self) -> None:
        self.assertEqual(prepare_text("extraordinario", max_chars=5), "extraordinario")


if __name__ == "__main__":
    unittest.main()
