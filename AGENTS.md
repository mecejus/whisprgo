# whisprgo

Hold-fn voice dictation for macOS. A single Go binary, cgo against Apple
frameworks, run as a launchd agent.

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

### You cannot compile this project in a cloud session

The session runs on Linux. `main`, `keyboard`, `paste`, `dialog` and the
recorder/player half of `audio` are cgo against CoreGraphics, AudioToolbox,
AppKit and ApplicationServices, so they build **only** on macOS.
Cross-compiling from here fails — there is no osxcross and no macOS SDK.

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
   compiler.** Expect to iterate through it; that is the normal workflow, not a
   failure.

Do not claim code compiles until a run says so.

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
| `release` | `ubuntu-latest` | `build` succeeding **and** `github.ref == refs/heads/main` |

`build` runs gofmt, `go vet`, `go test`, the cgo build, and a smoke test, then
uploads the binary as a run artifact. So a push to any branch gets you a real
compile and a downloadable binary without publishing anything.

`release` downloads that same artifact — it never rebuilds — then deletes and
recreates the `rolling-release` tag and its GitHub release. `install.sh`
resolves `/releases/latest`, which follows that tag, so a merge to `main` is
live for every user immediately. There is no staging step.

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
design. The audio is uploaded *while* the user speaks; the release sends only
the last frame and waits for the API's answer, then pastes natively. Keep
every step below off that path.

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
