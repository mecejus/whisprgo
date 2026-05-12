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
	"whisprgo/groq"
	"whisprgo/keyboard"
	"whisprgo/paste"
	"whisprgo/secret"
	"whisprgo/tts"
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
	fmt.Println("A system dialog will ask for Accessibility access. Grant it,")
	fmt.Println("then restart the service to apply:")
	fmt.Println("  brew services restart whisprgo")
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

	// macOS caches Accessibility denials per process: granting access mid-run
	// doesn't take effect, and exiting risks a launchd respawn loop that
	// re-fires the dialog. Trigger the prompt once, print the restart
	// instruction, and block on signals — `brew services restart whisprgo`
	// will kill us and the fresh process will see the grant.
	if !keyboard.HasAccess() {
		keyboard.PromptForAccess()
		fmt.Fprintln(os.Stderr, "Accessibility access required.")
		fmt.Fprintln(os.Stderr, "Grant it in the system dialog, then run:")
		fmt.Fprintln(os.Stderr, "  brew services restart whisprgo")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		return
	}

	recorder, err := audio.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "audio init error: %v\n", err)
		os.Exit(1)
	}
	defer recorder.Close()

	client := &groq.Client{APIKey: cfg.APIKey}
	speaker := &tts.Client{APIKey: cfg.APIKey}

	var isRecording atomic.Bool

	onStart := func(mode keyboard.Mode) {
		if !isRecording.CompareAndSwap(false, true) {
			return
		}
		// Interrupt any prior agent response that's still speaking so a new
		// press is responsive.
		speaker.Stop()
		if err := recorder.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "\nrecorder start: %v\n", err)
			isRecording.Store(false)
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

		if mode == keyboard.ModeAgent {
			fmt.Printf("\r\033[K? %s\n", text)
			fmt.Print("◌ Thinking...")
			answer, err := client.Ask(text)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nllm: %v\n", err)
				return
			}
			answer = strings.TrimSpace(answer)
			if answer == "" {
				fmt.Print("\r\033[K(no response)\n")
				return
			}
			fmt.Printf("\r\033[K✓ %s\n", answer)
			if err := speaker.Speak(answer); err != nil {
				fmt.Fprintf(os.Stderr, "tts: %v\n", err)
			}
			return
		}

		fmt.Printf("\r\033[K✓ %s\n", text)
		if err := paste.Paste(text); err != nil {
			fmt.Fprintf(os.Stderr, "paste: %v\n", err)
		}
	}

	if err := keyboard.Start(onStart, onEnd); err != nil {
		fmt.Fprintf(os.Stderr, "keyboard hook: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("whisprgo ready — hold [fn] to dictate, double-press to ask. Ctrl-C to quit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nBye.")
}
