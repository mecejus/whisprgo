package main

import (
	"fmt"
	"os"
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

const (
	soundStart = 0
	soundEnd   = 1
)

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
	// re-fires the dialog. Trigger the system prompt once and block on
	// signals — a `launchctl kickstart -k` will kill us and the fresh process
	// will see the grant. We deliberately do NOT raise our own dialog here:
	// doing so steals focus from the System Settings window the prompt
	// deeplinks to, which confuses users.
	if !keyboard.HasAccess() {
		keyboard.PromptForAccess()
		fmt.Fprintln(os.Stderr, "Accessibility access is required. Grant it in System Settings, then run: launchctl kickstart -k \"gui/$(id -u)/com.whisprgo\"")
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

	player, err := audio.NewPlayer()
	if err != nil {
		fatal(fmt.Sprintf("Audio player init error: %v", err))
	}
	defer player.Close()
	if err := player.Load(soundStart, "/System/Library/Sounds/Blow.aiff"); err != nil {
		fatal(fmt.Sprintf("Load start sound: %v", err))
	}
	if err := player.Load(soundEnd, "/System/Library/Sounds/Bottle.aiff"); err != nil {
		fatal(fmt.Sprintf("Load end sound: %v", err))
	}

	client := &groq.Client{APIKey: cfg.APIKey}

	var isRecording atomic.Bool

	onStart := func() {
		if !isRecording.CompareAndSwap(false, true) {
			return
		}
		if err := recorder.Start(); err != nil {
			isRecording.Store(false)
			dialog.Error(fmt.Sprintf("Recorder start error: %v", err))
			return
		}
		player.Play(soundStart)
		fmt.Print("\r\033[K● Recording...")
	}

	onEnd := func() {
		if !isRecording.CompareAndSwap(true, false) {
			return
		}
		player.Play(soundEnd)
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

		fmt.Printf("\r\033[K✓ %s\n", text)
		if err := paste.Paste(text); err != nil {
			dialog.Error(fmt.Sprintf("Paste error: %v", err))
		}
	}

	if err := keyboard.Start(onStart, onEnd); err != nil {
		fatal(fmt.Sprintf("Keyboard hook error: %v", err))
	}

	fmt.Println("whisprgo ready — hold [fn] to dictate. Ctrl-C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nBye.")
}
