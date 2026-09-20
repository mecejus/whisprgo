# whisprgo

Hold-a-key voice dictation. One Go module, two binaries:

- **macOS** — hold fn. cgo against Apple frameworks, run as a launchd agent.
- **Windows** — hold ctrl + win. Pure Go against Win32, run from the per-user
  Run key.

The platform-neutral half (the FLAC encoder, the capture buffer, the streaming
Groq client, config) is shared; `keyboard`, `paste`, `dialog` and the
recorder/player halves of `audio` are split by build tag.

## Output style

Apply the `i-have-adhd` skill (`.agents/skills/i-have-adhd/SKILL.md`) to every
response in this repository, from the first turn, without waiting to be asked.
Read that file at session start and follow it. It stays on until the user says
"stop adhd mode" or "normal mode".

## Delivery workflow

The loop is: the user prompts a cloud session, it builds the thing, opens a PR,
squash-merges it, the merge publishes a release, the branch auto-deletes, the
user moves on. Do the whole loop — a merged PR with a red or unwatched build is
not finished work. Watch the run to green before reporting done.

**There is no local machine in this loop.** Never propose a step that needs the
user's laptop — no local `go build`, no Xcode, no manual `gh release`. The one
thing only they can do is install the published binary on their own Mac and
speak into it; everything up to that point happens here.

### Land work through a PR, not a push to main

Always open a PR from the working branch and merge that, even for a one-line
change and even when the branch would fast-forward cleanly. Ask before merging;
the user may want to read the diff first.

Pushing straight to `main` produces the same commits but skips the two things
the PR is there for:

- **The branch is never cleaned up.** Auto-delete-on-merge fires on a *PR
  merge*. A fast-forward push leaves the branch behind, already merged, looking
  stale forever. Someone then has to delete it by hand.
- **There is no reviewable artifact.** The PR is where CI results, the diff and
  the decision to ship are recorded together.

Squash-merge unless the branch's individual commits are worth keeping.

After the merge, do not keep working on the old branch — it no longer exists on
the remote. Restart it from `main` as described under Branch auto-deletion.

### You cannot compile the macOS build in a cloud session. You can compile the Windows one.

The session runs on Linux. The darwin files in `main`, `keyboard`, `paste`,
`dialog` and `audio` are cgo against CoreGraphics, AudioToolbox, AppKit and
ApplicationServices, so they build **only** on macOS. Cross-compiling them
from here fails — there is no osxcross and no macOS SDK.

The Windows files have no cgo at all. They reach Win32 through
`syscall.LazyProc` and COM through hand-written vtable structs, which is a
deliberate constraint, not an accident: it is what makes the whole Windows
binary buildable and vettable from here.

    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/w.exe .
    GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/w-arm.exe .
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...

Run all three before pushing a Windows change. `go vet` matters more here than
usual: its `unsafeptr` check is what catches a uintptr from a syscall being
turned back into a pointer, which is the classic way to write a Win32 binding
that works until the GC moves under it.

Keep it that way. Reaching for cgo on the Windows side would cost the one
thing that makes this half of the project practical to develop here.

What this means in practice:

1. `gofmt -l .` works here. Use it before every push.
2. `CGO_ENABLED=0 go test ./audio/ ./groq/ ./config/` works here and is the
   real test loop: with cgo off the Apple-framework files drop out and the
   pure-Go parts (the FLAC encoder, the capture buffer, the streaming API
   client) build and test on Linux. `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0
   go build ./groq/ ./config/ ./audio/` typechecks the same files for the
   target. The FLAC round-trip tests decode with `afconvert` on macOS and the
   reference `flac` tool elsewhere (`apt-get install flac`), and skip if
   neither exists — install one so they run.
3. Everything else is verified by pushing and reading CI. **CI is the
   compiler** for the macOS half. Expect to iterate through it; that is the
   normal workflow, not a failure.
4. What no CI can check on either platform: the keyboard hook actually firing,
   a real microphone, and the paste landing in a real app. GitHub's Windows
   runners have no microphone, so `Prime` failing there is expected and
   non-fatal. Those need a real Mac and a real PC.

Do not claim macOS code compiles until a run says so.

### PowerShell is not compiled, so CI parses it

`install.ps1` and `uninstall.ps1` cannot be checked here — the session has no
PowerShell. The Windows CI job runs both through
`[System.Management.Automation.Language.Parser]::ParseFile`, which catches a
syntax error that would otherwise only surface on a user's PC. Do not weaken
that step.

### Branch auto-deletion

GitHub deletes the head branch on merge. Two consequences when the same session
continues after a merge:

- `git fetch origin <branch>` fails with `couldn't find remote ref`.
- The stale local remote-tracking ref makes `git push --force-with-lease` fail
  with `stale info`, which looks like a race but is not one.

Restart the branch from the merged default branch instead. Never stack new
commits on already-merged history:

    git remote prune origin
    git fetch origin main
    git checkout -B <branch> origin/main
    git push -u origin <branch>          # plain push; the branch is new again

### What a merge actually publishes

One workflow, `.github/workflows/build.yml`:

| Job | Runs on | Fires on |
|---|---|---|
| `build` | `macos-latest` (Apple Silicon) | every branch push, every PR, `workflow_dispatch` |
| `build-windows` | `windows-latest` | same |
| `release` | `ubuntu-latest` | **both** builds succeeding **and** `github.ref == refs/heads/main` |

`release` needing both builds is deliberate: one rolling release carries all
three binaries, so publishing after a half-failed build would leave a release
whose Windows asset is stale or missing, and `install.ps1` resolves that asset
by name. The cost is that a red Windows job also blocks a macOS release.

`build` runs gofmt, `go vet`, `go test`, the cgo build, and a smoke test, then
uploads the binary as a run artifact. So a push to any branch gets you a real
compile and a downloadable binary without publishing anything.

`release` downloads those same artifacts — it never rebuilds — then deletes
and recreates the `rolling-release` tag and its GitHub release. `install.sh`
resolves `/releases/latest` and `install.ps1` fetches
`/releases/latest/download/<asset>`; both follow that tag, so a merge to
`main` is live for every user immediately. There is no staging step.

Runs share one concurrency group per ref. A superseded branch build is
cancelled; a run on `main` is not, so a release can never be interrupted
between deleting and recreating the tag.

### Release operations

Dispatch the workflow, do not hand the user commands. `./release.sh` is a
one-line `gh workflow run build.yml --ref main`; it compiles nothing locally.

To get a binary for testing **without** publishing: push a branch and download
the artifact from that run. Do not push to `main` to test something.

### The smoke test is load-bearing

`AXIsProcessTrusted()` returns true on GitHub's runners, so the smoke test
exercises the whole startup path — AudioQueue prime, AudioUnit player, chime
decode, event tap — and the binary prints its ready line. If a change breaks
audio or keyboard init, CI catches it.

Two traps when editing that step:

- macOS ships **no** `timeout` command. Using it makes the step exit 127 and
  pass without running anything. Background the binary and poll with `kill -0`.
- A step that only greps for a success marker is silent on a crash. Assert on
  the exit code too.

### Credentials

The repository needs none. `release` uses the automatic `github.token`; no
secrets are configured and none should be added.

The user's Groq API key lives in `~/.config/whisprgo/config.json` on their Mac
and is prompted for on first launch. It must never reach CI, the repository, or
a workflow input. The smoke test writes a dummy key to get past the prompt.

### Code signing happens on the user's Mac, not in CI

There is no Windows counterpart to any of this. A keyboard hook needs no
grant, so nothing has to survive an upgrade, and a self-signed Authenticode
certificate earns no SmartScreen reputation — signing there would be work for
nothing. `install.ps1` calls `Unblock-File` and that is the whole story.

macOS ties an Accessibility grant to the binary's code signature. Go signs ad
hoc, which binds the grant to one build's hash, so an upgrade used to need the
grant removed and re-added by hand. `install.sh` therefore creates a
code-signing certificate on the user's Mac (its own keychain,
`whisprgo-signing.keychain-db`) and signs every downloaded binary with it, so
the grant survives upgrades. The published artifact stays ad hoc signed.

CI proves that recipe: the `Sign with the installer's recipe` step runs
`install.sh sign` against the fresh build on a throwaway keychain, asserts the
designated requirement is identifier plus certificate and identical across two
different binaries, and the smoke test runs the signed copy (Apple Silicon
kills a binary with a bad signature). Change the signing code only together
with that step.

## Code notes

The latency budget after the key is released is the whole point of the
design, on both platforms. The audio is uploaded *while* the user speaks; the release sends only
the last frame and waits for the API's answer, then pastes natively. Keep
every step below off that path.

### macOS

- `keyboard/hook_darwin.go` — the event-tap callback must stay non-blocking. It
  pushes onto a channel and returns. Anything slow (network, subprocess, modal
  dialog) goes on a worker behind it, or macOS disables the tap.
- `audio/recorder.go` — the AudioQueue is created once and reused. Do not go
  back to creating and disposing it per keypress. `Start` hands out a
  `Capture` that consumers read while it is still filling.
- `audio/capture.go` — the live sample buffer plus wake-ups. `StreamFLAC`
  follows it and emits a frame every 256 ms; that is what the upload is
  built on.
- `audio/flac.go` — a pure-Go FLAC encoder (fixed predictors, Rice coding).
  It exists because the upload streams frame by frame, which CoreAudio's
  file-based encoder cannot do. Keep it lossless; transcription accuracy is
  not worth trading for bytes. Never trust a change to it without the
  external-decoder round-trip test passing.
- `groq/client.go` — `Transcribe` streams the request body as the recording
  is captured (no `Content-Length`; chunked on HTTP/1.1, DATA frames on
  HTTP/2). If the API refuses that form it falls back to a buffered upload
  and remembers to skip streaming for the rest of the process; a transport
  hiccup falls back for that one dictation only. The live API cannot be
  reached from CI or the cloud session, so that fallback is the safety net
  for behaviour we cannot test here. `Warm` on key-down keeps a TLS
  connection pooled.
- `paste/paste_darwin.go` — the pasteboard is driven through NSPasteboard,
  not `pbcopy`/`pbpaste`. Each of those was a process spawn on the paste
  path, and pbcopy also needed a forced UTF-8 locale under launchd; NSString
  round-trips UTF-8 losslessly on its own. Do not reintroduce subprocesses
  here.

### Windows

- `keyboard/hook_windows.go` — a `WH_KEYBOARD_LL` hook, installed on a locked
  thread that then pumps messages forever, because Windows will not call a
  low-level hook on a thread that is not pumping. The callback has the same
  rule as the macOS event tap and a harsher penalty: exceed
  `LowLevelHooksTimeout` and Windows silently stops calling you for later
  events, with no notification and no disabled-tap callback to re-enable from.
  It holds no state at all — it classifies the event, pushes onto a channel
  and returns.
  It must ignore its own `SendInput` events by `dwExtraInfo`: without that,
  the ctrl posted by `paste` reads as the user reaching for the hold key and
  starts a recording on every paste.
  `maskWin` is not optional. The default hold key is ctrl+win, and Windows
  opens the Start menu when the Windows key is released with no other key
  having gone down while it was held — which is exactly the shape of holding
  ctrl+win, since ctrl goes down *before* win. One keystroke of a virtual-key
  code nothing maps makes the shell see an ordinary combination instead. It
  runs on the dispatch goroutine, never inside the hook: injecting input from
  a low-level hook callback re-enters that hook on the thread Windows is
  already timing.
  The watchdog is not optional either. Ctrl+Win+L locks the workstation
  mid-combination and the key-ups land on the secure desktop, never reaching
  us; without a resync against `GetAsyncKeyState` the state machine would sit
  believing the keys are still held and record until the capture cap stopped
  it.
- `keyboard/holdkey.go` — parses `"ctrl+win"` into something the hook can test
  cheaply, plus the state machine that decides when a combination is complete.
  Deliberately **not** behind a build tag: it is the most intricate logic in
  the Windows half (order independence, either-side modifiers, auto-repeat,
  two equivalent keys held at once) and untagged it is tested on every
  platform rather than only on a Windows runner. The macOS binary never
  references it, so the linker drops it. Only a single-key hold key is ever
  swallowed — eating half a combination is how a modifier gets stuck on.
- `audio/recorder_windows.go` — WASAPI shared mode, event-driven. Initialized
  once and then started and stopped per dictation, same as the AudioQueue:
  `IAudioClient::Start` is also what lights Windows' microphone indicator, so
  a permanently running stream would show the user as permanently listened to.
  All COM lives on one locked thread; `Stop` signals through an atomic rather
  than the command channel, because the capture loop is not reading commands
  while it runs.
- `audio/convert.go` — downmix and resample, reached only when WASAPI refuses
  `AUTOCONVERT_PCM` and hands over the raw mix format instead. It box-averages
  rather than decimating: plain decimation folds everything above 8 kHz back
  into the speech band, and what the model then hears is not what was said.
  The resampling window carries across calls, so chunked input must convert
  identically to whole input — there is a test for exactly that, and it is
  the one part of the Windows build that is genuinely verified before CI.
- `audio/com_windows.go` — hand-written COM vtables. Each interface is a
  struct whose one field points at its vtable, which is how a COM object is
  laid out, so every out-parameter stays a typed Go pointer and no uintptr is
  ever converted back into one. `waveFormatExtensible` is spelled out flat
  rather than embedding `waveFormatEx`: Go pads the inner struct to 20 bytes
  where C packs it to 18, which would put every field after it at the wrong
  offset.
- `paste/paste_windows.go` — the clipboard through the Win32 API, not
  `Set-Clipboard`; starting a PowerShell host on the paste path would be far
  worse than the `pbcopy` spawn macOS already refuses. Win32 memory is copied
  with `RtlMoveMemory` rather than by reshaping a locked address into a Go
  slice, which keeps `go vet` honest about the uintptr.
- `dialog/dialog_windows.go` — the API key prompt borrows WinForms through
  Windows PowerShell, which is the one subprocess on the Windows side. It runs
  once, on first launch, never on the dictation path.
  Start it with `CreationFlags: CREATE_NO_WINDOW`, **never**
  `SysProcAttr.HideWindow`. HideWindow sets `STARTF_USESHOWWINDOW` with
  `SW_HIDE`, and that show state is then inherited by the first window the
  process creates — the dialog itself. It shipped that way once: the box was
  drawn invisibly, nobody could dismiss it, `ShowDialog` never returned and
  whisprgo hung at startup having printed nothing at all.
  Which is the second rule here: a failure in this function must say why.
  Every path used to `return "", false` silently, so a wedged prompt and a
  cancelled one looked identical, and the first bug report was "nothing
  happens". The call is also bounded by a timeout, because a prompt that
  cannot be answered must fail rather than hang.
- `platform_windows.go` — writes the embedded chimes beside the config on
  first run and never overwrites them, so a user's own WAVs survive upgrades.
  `--background` is what the Run key passes: it redirects output to the log
  and hides the console. Without the flag the binary behaves like the Mac one
  run by hand — live output, Ctrl-C to quit.
