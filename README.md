# Whispr Go

A high performance macOS voice dictation service, free alternative to [Whispr Flow](https://wisprflow.ai).

## Features

- **Voice Dictation:** Hold Fn, speak, the transcription is pasted into the focused field instantly.
- **Free & Fast:** Powered entirely by Groq's free-tier API for near-instant responses.
- **Native Integration:** Single binary, minimal footprint, designed for macOS.

## Requirements

- macOS on Apple Silicon (M1 or later)
- A free [Groq API key](https://console.groq.com)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/install.sh | sh
```

On first launch a dialog will prompt for your Groq API key. After saving it, the macOS Accessibility permission prompt will appear (required for Fn-key recording). Click **Open System Settings** and toggle whisprgo on, then restart the service:

```bash
launchctl kickstart -k "gui/$(id -u)/com.whisprgo"
```

## Usage

Hold **Fn** and speak — the transcription is pasted into the focused field the moment you release.

Optionally, disable the Fn key's default action: **System Settings → Keyboard → Press globe key to → Do Nothing**

### Why it's fast

The audio is compressed to FLAC and uploaded to Groq *while you are still
talking*. Releasing Fn sends only the last fraction of a second, so what you
wait for is the model's answer, not the upload — a long dictation costs no
more at the end than a short one. The text then goes onto the pasteboard
natively and Cmd+V is posted, with no helper processes in between. The log
shows the release-to-paste time for every dictation.

## Configuration

`~/.config/whisprgo/config.json` holds the API key and two optional keys:

```json
{
  "api_key": "gsk_...",
  "model": "whisper-large-v3-turbo",
  "language": "en"
}
```

- `model` — defaults to `whisper-large-v3`, the most accurate.
  `whisper-large-v3-turbo` answers a little sooner at a small cost in accuracy.
- `language` — an ISO-639-1 code. Naming your language lets the model skip
  detecting it. Leave it out to auto-detect.

Restart the service after editing:

```bash
launchctl kickstart -k "gui/$(id -u)/com.whisprgo"
```

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

## Logs

```bash
tail -f ~/.config/whisprgo/whisprgo.log
```

## Building

Releases are built on GitHub's Apple Silicon runners and published
automatically on every push to `main` — see
[`.github/workflows/build.yml`](.github/workflows/build.yml). Nothing needs to
be compiled locally. Every branch and pull request gets the same build, vet and
smoke test, with the binary attached to the run as an artifact.

To publish a release by hand:

```bash
./release.sh    # triggers the workflow; requires the gh CLI
```
