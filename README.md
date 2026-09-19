# Whispr Go

Talk instead of typing. Hold a key, speak, let go. The words appear where your
cursor is. Free, fast, no app window.

Hold **fn** on a Mac, **ctrl + win** on Windows. You need a free
[Groq API key](https://console.groq.com); the installer asks for it.

## Install

macOS — paste into Terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/install.sh | sh
```

Windows — paste into PowerShell:

```powershell
irm https://raw.githubusercontent.com/mecejus/whisprgo/main/install.ps1 | iex
```

Either one says **All set** when it is done. whisprgo runs from then on,
including after a restart. Paste the same line again to update.

On a Mac it also asks for Accessibility access. Tip: stop fn opening the emoji
picker under **System Settings → Keyboard → Press 🌐 key to → Do Nothing**.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.ps1 | iex
```

## Why it is fast

The audio is compressed and uploaded to Groq *while you are still talking*.
Releasing the key sends only the last fraction of a second, so what you wait
for is the model's answer, not the upload. The text is then pasted natively,
with no helper processes in between.
