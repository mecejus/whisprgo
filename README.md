# Whispr Go

Talk instead of typing. Hold a key, speak, let go — the words appear wherever
your cursor is.

## Install

macOS — paste into Terminal:

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/install.sh | sh
```

Windows — paste into PowerShell:

```powershell
irm https://raw.githubusercontent.com/mecejus/whisprgo/main/install.ps1 | iex
```

It asks for a free [Groq API key](https://console.groq.com), then says **All
set**. Run the same line again to update.

## Dictate

| | Hold to talk |
|---|---|
| macOS | **fn** |
| Windows | **ctrl** + **win** |

On a Mac, set **System Settings → Keyboard → Press 🌐 key to → Do Nothing** so
fn does not open the emoji picker.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.ps1 | iex
```
