package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

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

	// How many dictations may be in flight at once. Deep enough to absorb
	// rapid-fire dictation, shallow enough that a wedged network surfaces as
	// an error instead of unbounded memory growth.
	queueDepth = 8

	// How long to wait for the Accessibility grant before exiting so launchd
	// can start a fresh process that asks again. launchd throttles restarts
	// to one per ten seconds, so a shorter wait gains nothing.
	accessRetryDelay = 10 * time.Second
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

// job is one dictation, from the key going down to the text being pasted.
//
// Transcription starts the moment the recording is long enough to be
// speech, not when the key comes up: the audio is uploaded as it is captured,
// so by the release only the last fraction of a second is left to send and
// the API's answer is what the user waits for.
type job struct {
	capture *audio.Capture
	// released is the key-up time (UnixNano), for the latency shown in the log.
	released atomic.Int64
	text     string
	done     chan struct{}
}

func (j *job) run(client *groq.Client) {
	defer close(j.done)

	// Don't open a request for a stray tap.
	if n, ended := j.capture.Wait(minSamples); ended && n < minSamples {
		fmt.Print("\r\033[K(too short)\n")
		return
	}

	text, err := client.Transcribe(context.Background(), groq.Upload{
		Filename: "audio.flac",
		Stream: func(w io.Writer) error {
			return audio.StreamFLAC(w, j.capture)
		},
		Buffered: func() ([]byte, error) {
			j.capture.WaitDone()
			return audio.EncodeFLAC(j.capture.Samples())
		},
	})
	if err != nil {
		reportError("Transcription error: %v", err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		fmt.Print("\r\033[K(no speech detected)\n")
		return
	}
	j.text = text
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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	// macOS answers the Accessibility question once per process: a process
	// that was told "no" keeps hearing "no" for its whole life, however the
	// toggle changes. Polling never flips and re-exec keeps the PID and the
	// answer with it (both tried, both confirmed on a real Mac). Only a fresh
	// process sees the grant. So show the system prompt once, then exit every
	// few seconds and let launchd's KeepAlive start a fresh process to ask
	// again. The marker file stops each fresh process re-firing the dialog.
	// We deliberately do NOT raise our own dialog here: doing so steals focus
	// from the System Settings window the prompt deeplinks to.
	if !keyboard.HasAccess() {
		if config.MarkAccessPrompted() {
			keyboard.PromptForAccess()
			fmt.Fprintln(os.Stderr, "Waiting for Accessibility access: in System Settings > Privacy & Security > Accessibility, turn whisprgo on. (Already on? Turn it off and on again.) whisprgo starts by itself within seconds of the grant.")
		}
		select {
		case <-sig:
		case <-time.After(accessRetryDelay):
			fmt.Fprintln(os.Stderr, "Accessibility not granted yet; restarting to check again. (Started by hand rather than launchd? Run whisprgo again once access is granted.)")
		}
		return
	}
	config.ClearAccessPrompted()

	recorder, err := audio.New()
	if err != nil {
		fatal(fmt.Sprintf("Audio init error: %v", err))
	}
	defer recorder.Close()

	// Build the input queue now rather than on the first keypress, so the
	// first dictation is as responsive as the rest. Not fatal if it fails:
	// under launchd we are KeepAlive, so exiting here over a device that
	// isn't enumerated yet at login would just respawn-loop. Start() primes
	// on demand, and reports the error where the user can act on it.
	if err := recorder.Prime(); err != nil {
		fmt.Fprintf(os.Stderr, "Audio warm-up failed, retrying on first use: %v\n", err)
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

	client := &groq.Client{
		APIKey:   cfg.APIKey,
		Model:    cfg.Model,
		Language: cfg.Language,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "\r\033[K"+format+"\n", args...)
		},
	}

	// Pasting runs behind the key handlers so releasing fn never blocks the
	// next press. Jobs are queued in the order dictations began and pasted in
	// that order, however their transcriptions come back.
	jobs := make(chan *job, queueDepth)
	go func() {
		for j := range jobs {
			<-j.done
			if j.text == "" {
				continue
			}
			wait := time.Since(time.Unix(0, j.released.Load()))
			fmt.Printf("\r\033[K✓ %s  [%d ms after release]\n", j.text, wait.Milliseconds())
			if err := paste.Paste(j.text); err != nil {
				reportError("Paste error: %v", err)
			}
		}
	}()

	// onStart and onEnd run on the keyboard package's single dispatch
	// goroutine, so current needs no locking.
	var current *job

	onStart := func() {
		if current != nil {
			return
		}
		// Chime first: it's lock-free against an already-running output unit,
		// so it lands immediately and tells the user to start talking. Arming
		// the mic behind it costs nothing they can hear.
		player.Play(soundStart)
		capture, err := recorder.Start()
		if err != nil {
			reportError("Recorder start error: %v", err)
			return
		}
		// The upload starts a third of a second from now. Have the connection
		// ready for it.
		client.Warm()

		j := &job{capture: capture, done: make(chan struct{})}
		current = j
		select {
		case jobs <- j:
			go j.run(client)
		default:
			reportError("Transcription backlog is full — this recording will be discarded.")
		}
		fmt.Print("\r\033[K● Recording...")
	}

	onEnd := func() {
		j := current
		if j == nil {
			return
		}
		current = nil
		player.Play(soundEnd)
		j.released.Store(time.Now().UnixNano())
		recorder.Stop()
		fmt.Print("\r\033[K◌ Transcribing...")
	}

	if err := keyboard.Start(onStart, onEnd); err != nil {
		fatal(fmt.Sprintf("Keyboard hook error: %v", err))
	}

	fmt.Println("whisprgo ready — hold [fn] to dictate. Ctrl-C to quit.")

	<-sig
	fmt.Println("\nBye.")
}
