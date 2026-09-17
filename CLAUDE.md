# whisprgo

Hold-fn voice dictation for macOS. A single Go binary, cgo against Apple
frameworks, run as a launchd agent.

## Output style

Apply the `i-have-adhd` skill (`.claude/skills/i-have-adhd/SKILL.md`) to every
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

### You cannot compile this project in a cloud session

The session runs on Linux. Every package except `config` and `groq` is cgo
against CoreGraphics, AudioToolbox and ApplicationServices, so it builds
**only** on macOS. Cross-compiling from here fails — there is no osxcross and
no macOS SDK.

What this means in practice:

1. `gofmt -l .` works here. Use it before every push.
2. `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./groq/ ./config/` also
   works here and typechecks those two packages.
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

## Code notes

- `keyboard/hook_darwin.go` — the event-tap callback must stay non-blocking. It
  pushes onto a channel and returns. Anything slow (network, subprocess, modal
  dialog) goes on a worker behind it, or macOS disables the tap.
- `audio/recorder.go` — the AudioQueue is created once and reused. Do not go
  back to creating and disposing it per keypress.
- `audio/encode.go` — FLAC via AudioToolbox, WAV fallback. Keep it lossless;
  the upload is on the user's critical path but transcription accuracy is not
  worth trading for bytes.
- `paste/paste_darwin.go` — the `LANG`/`LC_CTYPE` handling on `pbcopy` is
  deliberate. Under launchd, without it, non-ASCII transcripts get mangled.
