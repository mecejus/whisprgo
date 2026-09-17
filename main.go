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

	// Ignore anything shorter than this; it's a stray tap, not speech.
	minSamples = audio.SampleRate / 3

	// How many finished recordings may wait on transcription. Deep enough to
	// absorb rapid-fire dictation, shallow enough that a wedged network
	// surfaces as an error instead of unbounded memory growth.
	queueDepth = 8
)

func fatal(message string) {
	dialog.Error(message)
	os.Exit(1)
}

// dialogOpen serializes error dialogs. osascript blocks until the user clicks
// OK, so a dialog must never run on the dictation path, and a burst of errors
// must not bury the screen in modal windows.
var dialogOpen atomic.Bool

func reportError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "\r\033[K%s\n", msg)
	if !dialogOpen.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer dialogOpen.Store(false)
		dialog.Error(msg)
	}()
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

	// Build the input queue now rather than on the first keypress, so the
	// first dictation is as responsive as the rest.
	if err := recorder.Prime(); err != nil {
		fatal(fmt.Sprintf("Audio init error: %v", err))
	}

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

	// Transcription runs behind the key handlers so releasing fn never blocks
	// the next press. A single worker keeps pastes in the order they were
	// dictated.
	jobs := make(chan []int16, queueDepth)
	go func() {
		for samples := range jobs {
			transcribe(client, samples)
		}
	}()

	var isRecording atomic.Bool

	onStart := func() {
		if !isRecording.CompareAndSwap(false, true) {
			return
		}
		// Chime first: it's lock-free against an already-running output unit,
		// so it lands immediately and tells the user to start talking. Arming
		// the mic behind it costs nothing they can hear.
		player.Play(soundStart)
		if err := recorder.Start(); err != nil {
			isRecording.Store(false)
			reportError("Recorder start error: %v", err)
			return
		}
		// We know an upload is coming in a few seconds. Get the connection up
		// while they talk.
		client.Warm()
		fmt.Print("\r\033[K● Recording...")
	}

	onEnd := func() {
		if !isRecording.CompareAndSwap(true, false) {
			return
		}
		player.Play(soundEnd)
		samples, err := recorder.Stop()
		if err != nil {
			reportError("Recorder stop error: %v", err)
			return
		}
		if len(samples) < minSamples {
			fmt.Print("\r\033[K(too short)\n")
			return
		}
		fmt.Print("\r\033[K◌ Transcribing...")
		select {
		case jobs <- samples:
		default:
			reportError("Transcription backlog is full — dropped a recording.")
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

func transcribe(client *groq.Client, samples []int16) {
	data, filename := audio.Encode(samples)

	text, err := client.Transcribe(data, filename)
	if err != nil {
		reportError("Transcription error: %v", err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		fmt.Print("\r\033[K(no speech detected)\n")
		return
	}

	fmt.Printf("\r\033[K✓ %s\n", text)
	if err := paste.Paste(text); err != nil {
		reportError("Paste error: %v", err)
	}
}
