# Whispr Go

Talk instead of typing, anywhere on your Mac. Hold **fn**, speak, let go. The
words appear where your cursor is. Free, fast, no app window.

You need a Mac with Apple Silicon and a free
[Groq API key](https://console.groq.com).

## Install

Paste this into Terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/install.sh | sh
```

It asks for your API key and for Accessibility access, then says **All set**.
whisprgo runs from then on, including after a restart.

Tip: stop fn from opening the emoji picker under
**System Settings → Keyboard → Press 🌐 key to → Do Nothing**.

## Update

Paste the install line again.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

## Why it is fast

The audio is compressed and uploaded to Groq *while you are still talking*.
Releasing fn sends only the last fraction of a second, so what you wait for is
the model's answer, not the upload. The text is then pasted natively, with no
helper processes in between.
