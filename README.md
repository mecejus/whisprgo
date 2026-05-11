# Whispr Go

A high-performance voice dictation service, free alternative to [Whispr Flow](https://wisprflow.ai).

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

A system dialog will pop up asking for Accessibility access (required for Fn-key recording). Click **Open System Settings** and toggle whisprgo on, then restart the service to apply:

```bash
brew services restart whisprgo
```

## Usage

| Action | Result |
|--------|--------|
| Hold **Fn** | Dictate — transcribe and paste on release |
| Double Tap **Fn** and hold | Ask the agent — transcribe, send to LLM, speak the reply on release |

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