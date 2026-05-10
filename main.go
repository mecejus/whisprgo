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

	if keyboard.PromptForAccess() {
		fmt.Println("Accessibility access already granted.")
		fmt.Println()
		fmt.Println("Start the service:")
		fmt.Println("  brew services start whisprgo")
		return
	}

	fmt.Println("A system dialog should now ask for Accessibility access.")
	fmt.Println("Click 'Open System Settings' and toggle whisprgo on.")
	fmt.Println()
	fmt.Println("Then start the service:")
	fmt.Println("  brew services start whisprgo")
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

	for {
		if err := keyboard.Start(onPress, onRelease); err == nil {
			break
		}
		fmt.Fprintf(os.Stderr, "keyboard hook: accessibility access required\n")
		fmt.Fprintf(os.Stderr, "Grant access in: System Settings → Privacy & Security → Accessibility → add whisprgo\n")
		fmt.Fprintf(os.Stderr, "Retrying in 30s...\n")
		time.Sleep(30 * time.Second)
	}

	fmt.Println("whisprgo ready — hold [fn] to record, release to transcribe. Ctrl-C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nBye.")
}
