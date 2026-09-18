package groq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// form is what a handler saw of one transcription request.
type form struct {
	chunked  bool
	model    string
	language string
	filename string
	audio    []byte
}

func readForm(t *testing.T, r *http.Request) form {
	t.Helper()
	f := form{chunked: r.ContentLength < 0}
	mr, err := r.MultipartReader()
	if err != nil {
		t.Errorf("multipart: %v", err)
		return f
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return f
		}
		if err != nil {
			t.Errorf("part: %v", err)
			return f
		}
		b, _ := io.ReadAll(p)
		switch p.FormName() {
		case "model":
			f.model = string(b)
		case "language":
			f.language = string(b)
		case "file":
			f.filename = p.FileName()
			f.audio = b
		}
	}
}

func reply(w http.ResponseWriter, text string) {
	json.NewEncoder(w).Encode(map[string]string{"text": text})
}

func newClient(srv *httptest.Server) *Client {
	transcribeURL = srv.URL + "/transcriptions"
	return &Client{APIKey: "test-key", Language: "en", Logf: func(string, ...any) {}}
}

// The streamed upload must reach the server while the client is still
// producing it. The handler reads the first bytes of the file and only then
// lets the client finish, which deadlocks if anything buffers the body.
func TestTranscribeStreamsWhileRecording(t *testing.T) {
	firstBytesSeen := make(chan struct{})
	var got form
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.chunked = r.ContentLength < 0
		if a := r.Header.Get("Authorization"); a != "Bearer test-key" {
			t.Errorf("Authorization = %q", a)
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FormName() != "file" {
				b, _ := io.ReadAll(p)
				if p.FormName() == "model" {
					got.model = string(b)
				}
				continue
			}
			got.filename = p.FileName()
			head := make([]byte, 5)
			if _, err := io.ReadFull(p, head); err != nil {
				t.Errorf("reading head: %v", err)
				return
			}
			close(firstBytesSeen)
			rest, _ := io.ReadAll(p)
			got.audio = append(head, rest...)
		}
		reply(w, "hello world")
	}))
	defer srv.Close()
	c := newClient(srv)

	var buffered atomic.Bool
	text, err := c.Transcribe(context.Background(), Upload{
		Filename: "audio.flac",
		Stream: func(w io.Writer) error {
			if _, err := w.Write([]byte("fLaCx")); err != nil {
				return err
			}
			select {
			case <-firstBytesSeen:
			case <-time.After(5 * time.Second):
				return errors.New("server never saw the first bytes; the body was buffered")
			}
			_, err := w.Write([]byte("more-audio-after-release"))
			return err
		},
		Buffered: func() ([]byte, error) {
			buffered.Store(true)
			return nil, errors.New("must not be called")
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q", text)
	}
	if buffered.Load() {
		t.Fatal("fell back to a buffered upload")
	}
	if !got.chunked {
		t.Error("request carried a Content-Length; it should have been streamed")
	}
	if got.model != DefaultModel {
		t.Errorf("model = %q, want %q", got.model, DefaultModel)
	}
	if got.filename != "audio.flac" {
		t.Errorf("filename = %q", got.filename)
	}
	if string(got.audio) != "fLaCxmore-audio-after-release" {
		t.Errorf("audio = %q", got.audio)
	}
}

// A server that refuses bodies without a Content-Length gets the recording
// again the conventional way, and the client remembers not to try streaming
// on later dictations.
func TestTranscribeFallsBackWhenStreamingRefused(t *testing.T) {
	var requests, streamed atomic.Int32
	var last form
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.ContentLength < 0 {
			streamed.Add(1)
			http.Error(w, "Length Required", http.StatusLengthRequired)
			return
		}
		last = readForm(t, r)
		reply(w, "buffered ok")
	}))
	defer srv.Close()
	c := newClient(srv)
	c.Model = "whisper-large-v3-turbo"

	upload := func(streamCalls *int32) Upload {
		return Upload{
			Filename: "audio.flac",
			Stream: func(w io.Writer) error {
				atomic.AddInt32(streamCalls, 1)
				// Keep writing until the server's answer stops us; a real
				// recording is still going when a 411 comes back.
				for i := 0; i < 1000; i++ {
					if _, err := w.Write([]byte(strings.Repeat("a", 1024))); err != nil {
						return err
					}
					time.Sleep(time.Millisecond)
				}
				return errors.New("server never answered the streamed request")
			},
			Buffered: func() ([]byte, error) { return []byte("whole-recording"), nil },
		}
	}

	var streamCalls int32
	text, err := c.Transcribe(context.Background(), upload(&streamCalls))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "buffered ok" {
		t.Fatalf("text = %q", text)
	}
	if last.chunked || string(last.audio) != "whole-recording" {
		t.Fatalf("buffered request: chunked=%v audio=%q", last.chunked, last.audio)
	}
	if last.model != "whisper-large-v3-turbo" || last.language != "en" {
		t.Errorf("model=%q language=%q", last.model, last.language)
	}
	if streamCalls != 1 || streamed.Load() != 1 {
		t.Fatalf("stream attempts: local %d, server %d; want 1 each", streamCalls, streamed.Load())
	}

	// Second dictation: straight to buffered.
	if _, err := c.Transcribe(context.Background(), upload(&streamCalls)); err != nil {
		t.Fatalf("second Transcribe: %v", err)
	}
	if streamCalls != 1 {
		t.Fatalf("streaming was attempted again after the API refused it")
	}
	if n := requests.Load(); n != 3 {
		t.Fatalf("server saw %d requests, want 3", n)
	}
}

// A connection dropped mid-upload is not the API rejecting streaming: the
// recording is uploaded buffered, and the next one streams again.
func TestTranscribeFallsBackOnTransportFailure(t *testing.T) {
	var streamedRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength < 0 {
			if streamedRequests.Add(1) == 1 {
				// Kill the first streamed request at the socket.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				if tcp, ok := conn.(*net.TCPConn); ok {
					tcp.SetLinger(0)
				}
				conn.Close()
				return
			}
		}
		f := readForm(t, r)
		reply(w, string(f.audio))
	}))
	defer srv.Close()
	c := newClient(srv)

	up := Upload{
		Filename: "audio.flac",
		Stream: func(w io.Writer) error {
			for i := 0; i < 1000; i++ {
				if _, err := w.Write([]byte(strings.Repeat("s", 1024))); err != nil {
					return err
				}
				time.Sleep(time.Millisecond)
			}
			return errors.New("server never answered")
		},
		Buffered: func() ([]byte, error) { return []byte("buffered-audio"), nil },
	}
	text, err := c.Transcribe(context.Background(), up)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "buffered-audio" {
		t.Fatalf("text = %q, want the buffered fallback", text)
	}
	if c.noStream.Load() {
		t.Fatal("a dropped connection disabled streaming")
	}

	// Next dictation streams and succeeds.
	up.Stream = func(w io.Writer) error {
		_, err := w.Write([]byte("streamed-audio"))
		return err
	}
	text, err = c.Transcribe(context.Background(), up)
	if err != nil {
		t.Fatalf("second Transcribe: %v", err)
	}
	if text != "streamed-audio" {
		t.Fatalf("second text = %q, want the streamed upload", text)
	}
}

// A server error is transient; it must not disable streaming.
func TestTranscribeServerErrorDoesNotDisableStreaming(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		f := readForm(t, r)
		reply(w, string(f.audio))
	}))
	defer srv.Close()
	c := newClient(srv)

	up := Upload{
		Filename: "audio.flac",
		Stream: func(w io.Writer) error {
			_, err := w.Write([]byte("streamed"))
			return err
		},
		Buffered: func() ([]byte, error) { return []byte("buffered"), nil },
	}
	text, err := c.Transcribe(context.Background(), up)
	if err != nil || text != "buffered" {
		t.Fatalf("Transcribe = %q, %v", text, err)
	}
	if c.noStream.Load() {
		t.Fatal("a 503 disabled streaming")
	}
}

// Real errors surface with both attempts' causes and leave streaming on.
func TestTranscribeReportsRealErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := newClient(srv)

	_, err := c.Transcribe(context.Background(), Upload{
		Filename: "audio.flac",
		Stream: func(w io.Writer) error {
			_, err := w.Write([]byte("x"))
			return err
		},
		Buffered: func() ([]byte, error) { return []byte("x"), nil },
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "API 401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %v", err)
	}
	if c.noStream.Load() {
		t.Fatal("an auth failure disabled streaming")
	}
}

// Without a Stream func the client behaves as it always did.
func TestTranscribeBufferedOnly(t *testing.T) {
	var got form
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = readForm(t, r)
		reply(w, "plain")
	}))
	defer srv.Close()
	c := newClient(srv)

	text, err := c.Transcribe(context.Background(), Upload{
		Filename: "audio.flac",
		Buffered: func() ([]byte, error) { return []byte("pcm"), nil },
	})
	if err != nil || text != "plain" {
		t.Fatalf("Transcribe = %q, %v", text, err)
	}
	if got.chunked || string(got.audio) != "pcm" || got.filename != "audio.flac" {
		t.Fatalf("request: %+v", got)
	}
}
