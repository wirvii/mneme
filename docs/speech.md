# Local spoken responses

mneme can speak the useful part of an agent response without reading raw tool
output, code, progress chatter, or the entire visual answer. Speech is always
off after a first install and stays off after upgrades unless you explicitly
enabled it before the upgrade.

mneme speaks only with the native engine of the host operating system. It
never installs or downloads anything to speak:

- **macOS** uses the built-in `say` command. No setup required.
- **Windows** uses the built-in `System.Speech`/SAPI voices through a fixed
  PowerShell program. No setup required.
- **Linux** requires a locally installed `piper` binary and an existing Piper
  model that you configure explicitly (see below). mneme never downloads a
  model on your behalf.

```bash
mneme speech on
mneme speech status
mneme speech voice set --language es --engine system --voice Paulina \
  --fallback-engine system --fallback-voice Paulina
mneme speech mode brief
mneme speech mode full
mneme speech stop
mneme speech off
```

The `UserPromptSubmit` hook cancels speech from the previous turn **in that
same session** — never audio belonging to a different session, even when
several sessions share this host at once (see "Several sessions at once"
below) — and asks the agent to resolve the new turn exactly once through
`speech_emit`. The agent may emit useful prose or explicitly skip speech. If
it forgets to resolve its own previous turn before prompting again, mneme
remains silent and increments `missed_turns`; that counter only ever counts a
session's own unresolved turns, never a collision with another session's
prompt. It never reads the raw answer.

## Linux: configuring a local Piper model

mneme never downloads a Piper model. Point it at a model file you already
have on disk:

```bash
mneme speech setup --model /path/to/voice.onnx --sha256 EXPECTED_DIGEST
```

`mneme speech setup` always requires both `--model` and `--sha256`; it
verifies the file's checksum before accepting it and never fetches anything
over the network.

## Privacy and engines

Speech text stays on the host: it travels over an authenticated loopback socket
to one short-lived mneme supervisor, reaches the synthesizer through standard
input, and is never written to disk, logs, or process arguments. No cloud or
external TTS service is supported.

- macOS speaks with installed `say` voices.
- Windows speaks with installed System.Speech/SAPI voices through a fixed
  PowerShell program. Text is read from stdin and never interpolated into the
  command.
- Linux uses a locally installed `piper` binary and model, plus `aplay`,
  `paplay`, or `ffplay`.

`mneme speech voices` lists the voices installed on the host for a given
native engine.

Never two voices talk over each other: a second emission waits its turn
behind whatever is already playing instead of cutting it off (see "Several
sessions at once" below for what does cancel it).

## Several sessions at once

It is normal to have more than one `mneme`-backed agent session running on
the same machine at the same time. mneme accounts for that with a queue that
knows who owns each emission:

- **A queue, not a single slot.** Emissions from different sessions wait
  their turn and play one after another — nothing is dropped just because
  something else is already speaking.
- **One cancellation rule.** Typing in a session cancels **all** of that
  session's own audio: whatever is playing right now, and anything of it
  still waiting in the queue. Nothing belonging to any other session is
  touched. The reasoning: if you just typed in that session, you are looking
  at it and have already read what mattered there, so anything of yours still
  queued is stale — but a different session's audio is not stale just because
  you typed somewhere else.
- **Spoken provenance.** When an emission plays while you are not looking at
  its session (your last keystroke went to a different one), mneme says
  where it came from first — for example "In mneme: I finished the spec" —
  so a voice you did not expect never plays anonymously.
- **Silent expiry, with a cap.** A queued emission that waits too long is
  dropped without ever playing and without any summary or announcement: the
  text is already sitting in its own session's transcript, so re-announcing
  something old later would be exactly the noise this limit exists to avoid.
  The queue also caps how many emissions can wait at once, dropping the
  oldest pending one (never the one currently playing) when it is full.
- **What `spoken` means.** `speech_emit`'s `spoken:true` means the audio
  **started playing just now** — never that it finished. mneme does not wait
  out the whole narration before answering, because that would block the
  call for as long as the speech lasts. When an emission has to wait its
  turn instead, the response carries `queued:true` and `queue_position`
  (1 = next) rather than `spoken:true`.
- **Seeing the queue.** `mneme speech status` (and `speech_control status`)
  reports how many emissions are waiting, how many were discarded before
  they could play, and how many were cancelled because a session's own
  prompt cut them off. `degraded` now also turns true when the last
  synthesis failed, or when something was discarded before playing — on top
  of its original reason, a configured engine that does not match this
  host's own engine.

## Legacy configuration degrades, it never fails

An earlier mneme release shipped an additional managed engine that
downloaded and ran its own model. That engine has been retired (see the
CHANGELOG for its name and migration notes): mneme now speaks only with the
engines above. A `config.toml` written by that older mneme is never
rejected — any setting that still names the retired engine is automatically
rewritten to a native equivalent the next time the config is read or
written, and a warning is printed (to stderr, or surfaced as `warnings` in
`speech status --json` / `speech_control status`) explaining exactly what
changed:

- A top-level `[speech].engine` naming the retired engine falls back to
  `auto`.
- A `[speech.languages.<lang>]` preference naming the retired engine **and**
  declaring a `fallback_engine`/`fallback_voice` is promoted to that
  fallback engine and voice (this is the common case: an older config
  already named a native fallback for exactly this situation).
- A `[speech.languages.<lang>]` preference naming the retired engine with
  **no** declared fallback has its engine and voice cleared — a managed
  voice name means nothing on a native engine, so leaving it in place would
  be worse than no preference at all.
- A `[speech.languages.<lang>].fallback_engine` naming the retired engine is
  cleared the same way.

The configuration file cures itself the first time you change any speech
setting (`speech voice set`, `speech mode`, etc.): the rewritten, native-only
values are what gets persisted back to disk.
