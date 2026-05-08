# whisprgo

Hold **Fn** to record your voice. Release to transcribe. The text is instantly pasted wherever your cursor is.

Powered by [Groq](https://groq.com) + Whisper — transcription is typically done in under a second.

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
mkdir -p ~/.config/whisprgo
printf '{"api_key":"YOUR_GROQ_API_KEY"}' > ~/.config/whisprgo/config.json
chmod 600 ~/.config/whisprgo/config.json
```

Start the service:

```bash
brew services start whisprgo
```

Grant Accessibility access so the Fn-key hook works:

**System Settings → Privacy & Security → Accessibility → click + → add `/opt/homebrew/bin/whisprgo`**

## Usage

| Action | Result |
|--------|--------|
| Hold **Fn** | Start recording |
| Release **Fn** | Transcribe and paste |

## Uninstall

```bash
brew services stop whisprgo
brew uninstall whisprgo
```

## Logs

```bash
tail -f $(brew --prefix)/var/log/whisprgo.log
```
