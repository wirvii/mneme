# Local spoken responses

mneme can speak the useful part of an agent response without reading raw tool
output, code, progress chatter, or the entire visual answer. Speech is always
off after a first install and stays off after upgrades unless you explicitly
enabled it before the upgrade.

```bash
mneme speech on
mneme speech on --yes
mneme speech status
mneme speech voice set --language es --engine kokoro --voice ef_dora \
  --fallback-engine system --fallback-voice Paulina
mneme speech engine status kokoro
mneme speech mode brief
mneme speech mode full
mneme speech stop
mneme speech off
```

The `UserPromptSubmit` hook cancels speech from the previous turn and asks the
agent to resolve the new turn exactly once through `speech_emit`. The agent may
emit useful prose or explicitly skip speech. If it forgets, mneme remains
silent and increments `missed_turns`; it never reads the raw answer.

## Managed Kokoro setup

Kokoro is the preferred cross-platform engine. It runs locally on macOS,
Linux, and Windows; macOS Apple Silicon uses MLX, while the other supported
targets use the PyTorch CPU runtime. The engine executable and model are
versioned separately so upgrades can reuse an already verified model.

When Kokoro is preferred but missing, `mneme speech on` prints the exact setup
plan and exits without changing anything. The plan includes its digest, final
disk use, and temporary disk use. Run `mneme speech on --yes` to consent to
that plan. mneme then downloads only HTTPS catalog artifacts, checks size and
SHA-256, runs a real offline synthesis healthcheck, and atomically activates
the new generation. A failed install leaves the previous generation active.

Use `mneme speech on --native` to enable speech immediately with the native
fallback instead. Managed-engine operations are explicit:

```bash
mneme speech engine status kokoro
mneme speech engine rollback kokoro
mneme speech engine remove kokoro          # dry-run
mneme speech engine remove kokoro --apply
```

## Privacy and engines

Speech text stays on the host: it travels over an authenticated loopback socket
to one short-lived mneme supervisor, reaches the synthesizer through standard
input, and is never written to disk or logs. No cloud or external TTS service
is supported.

- Kokoro uses a managed local model and executable. Normal synthesis forces
  Hugging Face and Transformers offline mode and passes text through stdin.
- macOS can fall back to installed `say` voices.
- Windows can fall back to installed System.Speech/SAPI voices through a fixed PowerShell
  program. Text is read from stdin and never interpolated into the command.
- Linux uses a locally installed `piper` binary and model, plus `aplay`,
  `paplay`, or `ffplay`. Configure an existing model explicitly:

  ```bash
  mneme speech setup --model /path/to/voice.onnx --sha256 EXPECTED_DIGEST
  ```

`mneme speech setup --model ...` remains the manual Piper path and never
downloads anything. `speech on --yes` is the only CLI path that installs the
managed Kokoro plan. `mneme speech voices` lists native system voices; managed
Kokoro voices are selected with `speech voice set`.

Only one session owns audio at a time. A newer spoken response cancels the
current process before it starts, with no queue.
