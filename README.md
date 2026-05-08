# whisprgo

Hold **Fn** to record your voice. Release to transcribe. The text is instantly pasted wherever your cursor is.

Powered by [Groq](https://groq.com) + Whisper — transcription is typically done in under a second.

## Requirements

- macOS (Apple Silicon or Intel)
- [Go](https://go.dev) 1.21+
- [PortAudio](https://www.portaudio.com) (audio capture)
- A free [Groq API key](https://console.groq.com)

## Installation

### 1. Install dependencies

```bash
brew install go portaudio
```

### 2. Clone the repo

```bash
git clone https://github.com/mecejus/whisprgo.git
cd whisprgo
```

### 3. Run the install script

```bash
./install.sh
```

The script will:
- Build the binary from source
- Install it to `/usr/local/bin/whisprgo`
- Prompt for your Groq API key (only on first install)
- Register a login item that starts whisprgo automatically at login

### 4. Grant Accessibility access

The Fn-key hook requires Accessibility permission:

**System Settings → Privacy & Security → Accessibility → click + → add `/usr/local/bin/whisprgo`**

Once granted, whisprgo is ready — no restart needed.

## Usage

| Action | Result |
|--------|--------|
| Hold **Fn** | Start recording (you'll hear a short click) |
| Release **Fn** | Stop recording, transcribe, and paste (another click confirms) |

The transcribed text is pasted at your current cursor position in any app.

## Running manually (without installing as a service)

If you just want to try it in the terminal first:

```bash
go run main.go
```

You'll be prompted for your Groq API key on first run. It's saved to `~/.config/whisprgo/config.json` for future runs.

## Logs

When running as a background service, output is written to:

```
~/.config/whisprgo/whisprgo.log
```

Follow live:

```bash
tail -f ~/.config/whisprgo/whisprgo.log
```

## Uninstallation

```bash
./uninstall.sh
```

This stops the service, removes the login item, and deletes the binary. Your API key and logs at `~/.config/whisprgo/` are left untouched — delete that folder manually if you want a fully clean removal:

```bash
rm -rf ~/.config/whisprgo
```
