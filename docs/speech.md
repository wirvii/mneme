# Local spoken responses

mneme can speak the useful part of an agent response without reading raw tool
output, code, progress chatter, or the entire visual answer. Speech is always
off after a first install and stays off after upgrades unless you explicitly
enabled it before the upgrade.

```bash
mneme speech on
mneme speech status
mneme speech mode brief
mneme speech mode full
mneme speech stop
mneme speech off
```

The `UserPromptSubmit` hook cancels speech from the previous turn and asks the
agent to resolve the new turn exactly once through `speech_emit`. The agent may
emit useful prose or explicitly skip speech. If it forgets, mneme remains
silent and increments `missed_turns`; it never reads the raw answer.

## Privacy and engines

Speech text stays on the host: it travels over an authenticated loopback socket
to one short-lived mneme supervisor, reaches the synthesizer through standard
input, and is never written to disk or logs. No cloud or external TTS service
is supported.

- macOS uses installed `say` voices.
- Windows uses installed System.Speech/SAPI voices through a fixed PowerShell
  program. Text is read from stdin and never interpolated into the command.
- Linux uses a locally installed `piper` binary and model, plus `aplay`,
  `paplay`, or `ffplay`. Configure an existing model explicitly:

  ```bash
  mneme speech setup --model /path/to/voice.onnx --sha256 EXPECTED_DIGEST
  ```

mneme verifies the local model before saving it. Neither `mneme speech on` nor
`mneme speech setup` downloads anything. `mneme speech voices` lists system
voices on macOS and Windows; Piper models are local files rather than a system
voice catalog.

Only one session owns audio at a time. A newer spoken response cancels the
current process before it starts, with no queue.
