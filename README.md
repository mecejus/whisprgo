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

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

## Logs

```bash
tail -f ~/.config/whisprgo/whisprgo.log
```
