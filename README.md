# Whispr Go

Talk instead of typing, anywhere on your Mac. Hold the **fn** key, say what you
want, let go. The words appear where your cursor is.

It is free: transcription runs on [Groq](https://groq.com)'s free tier, and
there is no account with us, no app window, no menu bar icon. One small
program runs quietly in the background.

## You need

- A Mac with Apple Silicon (M1 or newer), macOS 13 or newer
- A free Groq API key: sign up at [console.groq.com](https://console.groq.com),
  open **API Keys**, click **Create API Key**, and copy it

## Install

Open **Terminal** (press `Cmd + Space`, type `Terminal`, press Enter), paste
this line, and press Enter:

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/install.sh | sh
```

Then follow along. Three things happen:

1. **Your Mac asks for your password.** That is Terminal placing the program in
   its folder. Type it and press Enter (nothing shows while you type).
2. **A box asks for your Groq API key.** Paste it and click OK.
3. **macOS asks about Accessibility.** Click **Open System Settings** and switch
   **whisprgo** on. This is what lets it notice the fn key.

The Terminal window says **All set** when it is ready. That is it: you never
need to start whisprgo. It runs from now on, including after a restart.

## Use

1. Click where you want the text to go (a message, a document, a search box).
2. Hold **fn**, talk, let go.
3. Your words appear.

Tip: if pressing fn opens the emoji picker, turn that off under
**System Settings → Keyboard → Press 🌐 key to → Do Nothing**.

## Update

Paste the same install line again. Your key and settings stay. Nothing to
switch on again.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

This removes the program, your API key, and everything else it created. One
thing a script cannot do: macOS may still list whisprgo under
**System Settings → Privacy & Security → Accessibility**. Select it and press
the minus (−) button. Leaving it is harmless.

## If something is off

- **Nothing happens when I hold fn.** Open **System Settings → Privacy &
  Security → Accessibility** and check whisprgo is switched on. If it is on
  and still nothing, switch it off, press the minus (−) button to remove it,
  and run the install line again.
- **I want to see what it is doing.** In Terminal:
  `tail -f ~/.config/whisprgo/whisprgo.log`
- **I want to change my key or the language.** See below.

## Settings (optional)

Your settings live in `~/.config/whisprgo/config.json`:

```json
{
  "api_key": "gsk_...",
  "model": "whisper-large-v3-turbo",
  "language": "en"
}
```

- `model`: leave it out for `whisper-large-v3`, the most accurate.
  `whisper-large-v3-turbo` answers a little sooner at a small cost in accuracy.
- `language`: a two-letter code like `en`, `lt`, `de`. Naming your language
  lets the model skip detecting it. Leave it out to auto-detect.

After editing, restart whisprgo:

```bash
launchctl kickstart -k "gui/$(id -u)/com.whisprgo"
```

## For developers

### Why it is fast

The audio is compressed to FLAC and uploaded to Groq *while you are still
talking*. Releasing fn sends only the last fraction of a second, so what you
wait for is the model's answer, not the upload. The text then goes onto the
pasteboard natively and Cmd+V is posted, with no helper processes in between.
The log shows the release-to-paste time for every dictation.

### Building and releasing

Releases are built on GitHub's Apple Silicon runners and published
automatically on every merge to `main`; see
[`.github/workflows/build.yml`](.github/workflows/build.yml). Nothing needs to
be compiled locally. Every branch and pull request gets the same build, vet and
smoke test, with the binary attached to the run as an artifact.

The installer signs the downloaded binary with a certificate it creates on
your Mac, so the Accessibility grant carries over between versions. CI runs
that exact signing recipe against each build. The details are in
[`AGENTS.md`](AGENTS.md).
