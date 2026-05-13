# Whispr Go

A high-performance voice dictation and agent for macOS, free alternative to [Whispr Flow](https://wisprflow.ai).

## Features

- **Voice Dictation:** Hold Fn, speak, release, text is pasted instantly.
- **Voice Agent:** Double-tap Fn and hold, ask anything aloud, the answer is typed straight into the focused field.
- **Free & Fast:** Powered entirely by Groq's free-tier API for near-instant responses.
- **Native Integration:** Single binary, minimal footprint, designed for macOS.

## Requirements

- macOS on Apple Silicon (M1 or later)
- A free [Groq API key](https://console.groq.com)

## Install

```bash
brew tap mecejus/tap
brew install whisprgo
```

Start the service:

```bash
brew services start whisprgo
```

On first launch a dialog will prompt for your Groq API key. After saving it, the macOS Accessibility permission prompt will appear (required for Fn-key recording). Click **Open System Settings** and toggle whisprgo on, then restart the service to apply:

```bash
brew services restart whisprgo
```

## Usage

| Action | Result |
|--------|--------|
| Hold **Fn** | Dictate, transcribe and paste on release |
| Double Tap **Fn** and hold | Ask the agent, the answer is typed into the focused field on release |

Optionally, disable the Fn key's default action: **System Settings → Keyboard → Press globe key to → Do Nothing**

## Uninstall

```bash
brew services stop whisprgo
brew uninstall whisprgo
```

## Logs

```bash
tail -f $(brew --prefix)/var/log/whisprgo.log
```