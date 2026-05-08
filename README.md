# whisprgo

Hold **Fn** to record your voice. Release to transcribe. The text is instantly pasted wherever your cursor is.

Powered by [Groq](https://groq.com) + Whisper — transcription is typically done in under a second.

## Requirements

- macOS on Apple Silicon (M1 or later)
- A free [Groq API key](https://console.groq.com)

## Installation

### Recommended: Homebrew

```bash
brew tap mecejus/tap
brew install whisprgo
```

After installing:

**1. Save your Groq API key:**
```bash
mkdir -p ~/.config/whisprgo
printf '{"api_key":"YOUR_GROQ_API_KEY"}' > ~/.config/whisprgo/config.json
chmod 600 ~/.config/whisprgo/config.json
```

**2. Start the service:**
```bash
brew services start whisprgo
```

**3. Grant Accessibility access** (required for the Fn-key hook):

System Settings → Privacy & Security → Accessibility → click + → add `/opt/homebrew/bin/whisprgo`

Once granted, whisprgo is ready — no restart needed.

### Alternative: install script

If you'd rather not use Homebrew, clone the repo and run the script — it downloads a pre-built binary for your architecture (no Go required):

```bash
git clone https://github.com/mecejus/whisprgo.git
cd whisprgo
./install.sh
```

The script will:
- Download the latest pre-built binary for your architecture
- Install it to `/usr/local/bin/whisprgo`
- Prompt for your Groq API key (only on first install)
- Register a login item that starts whisprgo automatically at login

Then grant Accessibility access as in step 3 above.

> **Developers:** pass `--build-from-source` to compile from source instead of downloading a binary. Requires `brew install go portaudio`.

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

**Homebrew:**
```bash
brew services stop whisprgo
brew uninstall whisprgo
```

**Install script:**
```bash
./uninstall.sh
```

Either way, your API key and logs at `~/.config/whisprgo/` are left untouched — delete that folder manually for a fully clean removal:

```bash
rm -rf ~/.config/whisprgo
```
