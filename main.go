package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"whisprgo/audio"
	"whisprgo/config"
	"whisprgo/dialog"
	"whisprgo/groq"
	"whisprgo/keyboard"
	"whisprgo/paste"
)

func playSound(path string) {
	exec.Command("afplay", path).Start()
}

func fatal(message string) {
	dialog.Error(message)
	os.Exit(1)
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal(fmt.Sprintf("Error loading config: %v", err))
	}

	if cfg.APIKey == "" {
		key, ok := dialog.Prompt("Enter your Groq API key (get one at https://console.groq.com):")
		if !ok || strings.TrimSpace(key) == "" {
			fatal("A Groq API key is required to use whisprgo.")
		}
		cfg.APIKey = strings.TrimSpace(key)
		if err := config.Save(cfg); err != nil {
			fatal(fmt.Sprintf("Error saving config: %v", err))
		}
	}

	// macOS caches Accessibility denials per process: granting access mid-run
	// doesn't take effect, and exiting risks a launchd respawn loop that
	// re-fires the dialog. Trigger the prompt once, show the restart
	// instruction, and block on signals — `brew services restart whisprgo`
	// will kill us and the fresh process will see the grant.
	if !keyboard.HasAccess() {
		keyboard.PromptForAccess()
		dialog.Error("Accessibility access is required. Grant it in the system dialog, then run:\n\nbrew services restart whisprgo")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		return
	}

	recorder, err := audio.New()
	if err != nil {
		fatal(fmt.Sprintf("Audio init error: %v", err))
	}
	defer recorder.Close()

	client := &groq.Client{APIKey: cfg.APIKey}

	var isRecording atomic.Bool

	onStart := func(mode keyboard.Mode) {
		if !isRecording.CompareAndSwap(false, true) {
			return
		}
		if err := recorder.Start(); err != nil {
			isRecording.Store(false)
			dialog.Error(fmt.Sprintf("Recorder start error: %v", err))
			return
		}
		playSound("/System/Library/Sounds/Blow.aiff")
		if mode == keyboard.ModeAgent {
			fmt.Print("\r\033[K● Recording (agent)...")
		} else {
			fmt.Print("\r\033[K● Recording...")
		}
	}

	onEnd := func(mode keyboard.Mode) {
		if !isRecording.CompareAndSwap(true, false) {
			return
		}
		playSound("/System/Library/Sounds/Bottle.aiff")
		wavData, err := recorder.Stop()
		if err != nil {
			dialog.Error(fmt.Sprintf("Recorder stop error: %v", err))
			return
		}
		// skip recordings shorter than ~0.3 s
		if len(wavData) < 44+16000*2/3 {
			fmt.Print("\r\033[K(too short)\n")
			return
		}
		fmt.Print("\r\033[K◌ Transcribing...")
		text, err := client.Transcribe(wavData)
		if err != nil {
			dialog.Error(fmt.Sprintf("Transcription error: %v", err))
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			fmt.Print("\r\033[K(no speech detected)\n")
			return
		}

		if mode == keyboard.ModeAgent {
			fmt.Printf("\r\033[K? %s\n", text)
			fmt.Print("◌ Thinking...")
			answer, err := client.Ask(text)
			if err != nil {
				dialog.Error(fmt.Sprintf("LLM error: %v", err))
				return
			}
			answer = strings.TrimSpace(answer)
			if answer == "" {
				fmt.Print("\r\033[K(no response)\n")
				return
			}
			fmt.Printf("\r\033[K✓ %s\n", answer)
			dialog.Info(answer)
			return
		}

		fmt.Printf("\r\033[K✓ %s\n", text)
		if err := paste.Paste(text); err != nil {
			dialog.Error(fmt.Sprintf("Paste error: %v", err))
		}
	}

	if err := keyboard.Start(onStart, onEnd); err != nil {
		fatal(fmt.Sprintf("Keyboard hook error: %v", err))
	}

	fmt.Println("whisprgo ready — hold [fn] to dictate, double-press to ask. Ctrl-C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nBye.")
}
