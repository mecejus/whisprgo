# Whispr Go

Hold **Fn** to record your voice. Release to transcribe. The text is instantly pasted wherever your cursor is. 

A high-performance, free alternative to Whisper Flow, powered by **Whisper Large v3** via the [Groq API](https://groq.com).

## Features

- **Free & Fast:** Uses Groq's generous free-tier API for lightning-fast inference.
- **Model:** Leverages OpenAI's **Whisper Large v3** for industry-leading accuracy.
- **Native Integration:** Single binary, minimal footprint, designed for macOS.

## Requirements

- macOS on Apple Silicon (M1 or later)
- A free [Groq API key](https://console.groq.com)

## Install

```bash
brew tap mecejus/tap
brew install whisprgo
```

Save your Groq API key:

```bash
whisprgo init
```

Start the service:

```bash
brew services start whisprgo
```

On first start, a system dialog will pop up asking for Accessibility access (required for Fn-key recording). Click **Open System Settings** and toggle whisprgo on — the service picks up the change automatically.

## Usage

| Action | Result |
|--------|--------|
| Hold **Fn** | Start recording |
| Release **Fn** | Transcribe and paste |

Optionally, disable the Fn key's default action:

**System Settings → Keyboard → Press globe key to → Do Nothing**

## Uninstall

```bash
brew services stop whisprgo
brew uninstall whisprgo
```

## Logs

```bash
tail -f $(brew --prefix)/var/log/whisprgo.log
```