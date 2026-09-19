# Whispr Go

Talk instead of typing. Hold a key, speak, let go. The words appear where your
cursor is. Free, fast, no app window.

| | Hold | You need |
|---|---|---|
| **macOS** | **fn** | Apple Silicon |
| **Windows** | **right ctrl** | 64-bit Windows 10 or 11 |

Both need a free [Groq API key](https://console.groq.com). The installer asks
for it on first run.

## Install — macOS

Paste this into Terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/install.sh | sh
```

It asks for your API key and for Accessibility access, then says **All set**.
whisprgo runs from then on, including after a restart.

Tip: stop fn from opening the emoji picker under
**System Settings → Keyboard → Press 🌐 key to → Do Nothing**.

## Install — Windows

Paste this into PowerShell:

```powershell
irm https://raw.githubusercontent.com/mecejus/whisprgo/main/install.ps1 | iex
```

It asks for your API key, then says **All set**. whisprgo runs from then on,
including after a restart. Nothing here needs administrator rights.

There is no fn key on Windows — on nearly all laptops it is handled inside the
keyboard and never reaches the OS — so the hold key is **right ctrl**.
whisprgo hides that key from whatever app is in front while you hold it, so
dictating cannot disturb what you are working in. The trade is that right
ctrl stops working as a shortcut key; see **Settings** below to change either
of those.

If Windows asks about microphone access, it needs to be on under
**Settings → Privacy & security → Microphone**, including **Let desktop apps
access your microphone**.

## Update

Paste the same install line again.

## Uninstall

macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

Windows:

```powershell
irm https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.ps1 | iex
```

## Settings

`~/.config/whisprgo/config.json` on both platforms (`C:\Users\you\.config\whisprgo\config.json`
on Windows). Everything but the key is optional:

```json
{
  "api_key": "gsk_...",
  "model": "whisper-large-v3-turbo",
  "language": "en",
  "hold_key": "capslock",
  "pass_through_hold_key": true
}
```

- `model` — defaults to `whisper-large-v3`, the most accurate.
  `whisper-large-v3-turbo` answers a little sooner.
- `language` — an ISO-639-1 hint like `"en"`. Naming it lets the model skip
  detecting it. Empty means auto-detect.
- `hold_key` — **Windows only.** `rightctrl` (default), `leftctrl`,
  `rightshift`, `rightalt`, `rightwin`, `menu`, `capslock`, `pause`,
  `scrolllock`, or `f13` through `f24`.
- `pass_through_hold_key` — **Windows only.** Leaves the hold key working
  normally in other apps. Off by default.

Restart whisprgo after editing (log out and back in, or run the install line
again).

## Why it is fast

The audio is compressed and uploaded to Groq *while you are still talking*.
Releasing the key sends only the last fraction of a second, so what you wait
for is the model's answer, not the upload. The text is then pasted natively,
with no helper processes in between.
