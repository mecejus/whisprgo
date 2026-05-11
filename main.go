package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"whisprgo/audio"
	"whisprgo/config"
	"whisprgo/groq"
	"whisprgo/keyboard"
	"whisprgo/paste"
	"whisprgo/secret"
)

func playSound(path string) {
	exec.Command("afplay", path).Start()
}

func runInit() {
	key, err := secret.Read("Paste your Groq API key: ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading key: %v\n", err)
		os.Exit(1)
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "API key cannot be empty")
		os.Exit(1)
	}
	if err := config.Save(&config.Config{APIKey: key}); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("API key saved to ~/.config/whisprgo/config.json")
	fmt.Println()
	fmt.Println("Start the service:")
	fmt.Println("  brew services start whisprgo")
	fmt.Println()
	fmt.Println("On first start, a system dialog will ask for Accessibility access.")
	fmt.Println("Click 'Open System Settings' and toggle whisprgo on — the service")
	fmt.Println("will pick up the change automatically.")
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInit()
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "No API key configured. Run: whisprgo init")
		os.Exit(1)
	}

	// Accessibility access is latched per process: granting it to a running
	// process doesn't take effect until the process restarts. So we prompt
	// once, poll silently until granted, then exit and let launchd respawn
	// us with a fresh process that picks up the new permission.
	if !keyboard.HasAccess() {
		fmt.Fprintln(os.Stderr, "Requesting Accessibility access — see the system dialog.")
		keyboard.PromptForAccess()
		for !keyboard.HasAccess() {
			time.Sleep(2 * time.Second)
		}
		fmt.Fprintln(os.Stderr, "Access granted. Restarting to apply.")
		os.Exit(0)
	}

	recorder, err := audio.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "audio init error: %v\n", err)
		os.Exit(1)
	}
	defer recorder.Close()

	client := &groq.Client{APIKey: cfg.APIKey}

	var isRecording atomic.Bool

	onPress := func() {
		if isRecording.CompareAndSwap(false, true) {
			if err := recorder.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "\nrecorder start: %v\n", err)
				isRecording.Store(false)
				return
			}
			playSound("/System/Library/Sounds/Tink.aiff")
			fmt.Print("\r\033[K● Recording...")
		}
	}
	onRelease := func() {
		if isRecording.CompareAndSwap(true, false) {
			playSound("/System/Library/Sounds/Pop.aiff")
			wavData, err := recorder.Stop()
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nrecorder stop: %v\n", err)
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
				fmt.Fprintf(os.Stderr, "\ntranscription: %v\n", err)
				return
			}
			text = strings.TrimSpace(text)
			if text == "" {
				fmt.Print("\r\033[K(no speech detected)\n")
				return
			}
			fmt.Printf("\r\033[K✓ %s\n", text)
			if err := paste.Paste(text); err != nil {
				fmt.Fprintf(os.Stderr, "paste: %v\n", err)
			}
		}
	}

	if err := keyboard.Start(onPress, onRelease); err != nil {
		fmt.Fprintf(os.Stderr, "keyboard hook: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("whisprgo ready — hold [fn] to record, release to transcribe. Ctrl-C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nBye.")
}
