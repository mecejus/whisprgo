package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"golang.org/x/term"

	"whisprgo/audio"
	"whisprgo/config"
	"whisprgo/groq"
	"whisprgo/keyboard"
	"whisprgo/paste"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if cfg.APIKey == "" {
		fmt.Print("Enter Groq API key: ")
		keyBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read key: %v\n", err)
			os.Exit(1)
		}
		cfg.APIKey = strings.TrimSpace(string(keyBytes))
		if cfg.APIKey == "" {
			fmt.Fprintln(os.Stderr, "API key cannot be empty")
			os.Exit(1)
		}
		if err := config.Save(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("API key saved to ~/.config/whisprgo/config.json")
	}

	recorder, err := audio.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "audio init error: %v\n", err)
		os.Exit(1)
	}
	defer recorder.Close()

	client := &groq.Client{APIKey: cfg.APIKey}

	var isRecording atomic.Bool

	if err := keyboard.Start(
		func() {
			if isRecording.CompareAndSwap(false, true) {
				if err := recorder.Start(); err != nil {
					fmt.Fprintf(os.Stderr, "\nrecorder start: %v\n", err)
					isRecording.Store(false)
					return
				}
				fmt.Print("\r\033[K● Recording...")
			}
		},
		func() {
			if isRecording.CompareAndSwap(true, false) {
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
		},
	); err != nil {
		fmt.Fprintf(os.Stderr, "keyboard hook: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "Grant access in: System Settings → Privacy & Security → Accessibility")
		fmt.Fprintln(os.Stderr, "Add your terminal app (Terminal / iTerm2), then relaunch whisprgo.")
		os.Exit(1)
	}

	fmt.Println("whisprgo ready — hold [fn] to record, release to transcribe. Ctrl-C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nBye.")
}
