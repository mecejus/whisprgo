package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"sync"
	"sync/atomic"
	"time"
)

var (
	transcribeURL = "https://api.groq.com/openai/v1/audio/transcriptions"
	modelsURL     = "https://api.groq.com/openai/v1/models"
)

const (
	// DefaultModel is used when the config names none. whisper-large-v3 is
	// the most accurate; whisper-large-v3-turbo answers a little sooner.
	DefaultModel = "whisper-large-v3"

	// A transcription that hasn't come back this long after the audio was
	// fully sent is not coming back. Without a bound, a half-open connection
	// wedges the worker until the process dies.
	requestTimeout = 90 * time.Second

	// A streamed upload lives as long as the recording plus the transcription.
	// This is the absolute ceiling, well past the recorder's own cap.
	streamCeiling = 10 * time.Minute

	// Re-warming more often than this is pointless; the pooled connection is
	// still good.
	warmInterval = 30 * time.Second
)

type Client struct {
	APIKey string

	// Model is the transcription model; empty means DefaultModel.
	Model string
	// Language is an optional ISO-639-1 hint ("en"). Naming it lets the model
	// skip language detection. Empty means auto-detect.
	Language string
	// Logf, if set, receives diagnostics worth a log line but not an error
	// dialog, such as a streamed upload falling back to a buffered one.
	Logf func(format string, args ...any)

	once     sync.Once
	http     *http.Client
	lastWarm atomic.Int64

	// noStream is set once the API has refused a streamed upload but accepted
	// the same audio buffered, so later dictations skip the doomed attempt.
	noStream atomic.Bool
}

// Upload describes one recording to transcribe.
type Upload struct {
	// Filename's extension tells the API the container format ("audio.flac").
	Filename string

	// Stream writes the encoded audio to w as it is captured and returns once
	// the recording has ended and its last byte is written. The upload runs
	// while the user is still speaking, so when the key is released only the
	// final fraction of a second remains to send. Optional.
	Stream func(w io.Writer) error

	// Buffered returns the complete encoded recording. It is the fallback
	// when streaming fails, and must block until the recording has ended.
	Buffered func() ([]byte, error)
}

type transcribeResponse struct {
	Text string `json:"text"`
}

func (c *Client) client() *http.Client {
	c.once.Do(func() {
		t := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2: true,
			// Only ever one host, and at most a couple of dictations in
			// flight — but keep the idle connection around far longer than
			// the 90 s default, since dictations come in bursts minutes apart.
			MaxIdleConns:        4,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     5 * time.Minute,
			TLSHandshakeTimeout: 10 * time.Second,
		}
		// No client-wide timeout: a streamed upload legitimately lasts as
		// long as the recording. Each request carries its own context.
		c.http = &http.Client{Transport: t}
	})
	return c.http
}

func (c *Client) model() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Warm opens a pooled TLS connection to the API so the upload that follows
// doesn't pay for DNS, TCP and the TLS handshake — commonly 150-500 ms, and
// worse on flaky wifi. Called when the fn key goes down, it runs while the
// user is still speaking, so its cost is invisible. Returns immediately and
// does nothing if a connection was warmed recently.
func (c *Client) Warm() {
	now := time.Now().UnixNano()
	last := c.lastWarm.Load()
	if now-last < int64(warmInterval) {
		return
	}
	if !c.lastWarm.CompareAndSwap(last, now) {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		resp, err := c.client().Do(req)
		if err != nil {
			return
		}
		// Drain and close, or the connection never makes it back to the pool
		// and the warm-up achieves nothing.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
}

// Transcribe uploads a recording and returns the recognized text.
//
// When the upload can be streamed it is: the request opens right away and
// the audio flows as it is captured, so the API has nearly all of it by the
// time the key is released. If that attempt fails for any reason the whole
// recording is uploaded again the conventional way, and if the API turned
// out to reject streamed requests outright, later calls go straight to the
// buffered upload.
func (c *Client) Transcribe(ctx context.Context, up Upload) (string, error) {
	if up.Stream == nil || c.noStream.Load() {
		return c.buffered(ctx, up)
	}

	text, serr := c.stream(ctx, up)
	if serr == nil {
		return text, nil
	}
	if ctx.Err() != nil {
		return "", serr
	}
	c.logf("streamed upload failed, retrying buffered: %v", serr)

	text, err := c.buffered(ctx, up)
	if err != nil {
		return "", fmt.Errorf("%w (streamed attempt: %v)", err, serr)
	}

	// The API answered the streamed request with a client error, then took
	// the same audio with a Content-Length. It dislikes the form, not the
	// audio; stop paying for the failed attempt on every dictation.
	var se *statusError
	if errors.As(serr, &se) && se.code >= 400 && se.code < 500 && se.code != http.StatusTooManyRequests {
		c.noStream.Store(true)
		c.logf("API refused a streamed upload (HTTP %d); uploading after the recording from now on", se.code)
	}
	return text, nil
}

// errResponded unblocks a streaming writer whose request already got an
// answer, which can only be an error since a success needs the whole body.
var errResponded = errors.New("server responded before the upload finished")

func (c *Client) stream(ctx context.Context, up Upload) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, streamCeiling)
	defer cancel()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transcribeURL, pr)
	if err != nil {
		return "", err
	}
	// Unknown length: HTTP/1.1 sends chunks, HTTP/2 sends DATA frames, and
	// Go flushes each write straight to the socket either way.
	req.ContentLength = -1
	c.setHeaders(req, mw.FormDataContentType())

	type outcome struct {
		text string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		text, err := c.do(req)
		// An early answer means the transport stopped reading the body.
		// Fail the writer's next Write rather than leaving it blocked.
		pr.CloseWithError(errResponded)
		done <- outcome{text, err}
	}()

	werr := c.writeForm(mw, up.Filename, up.Stream)
	if werr != nil {
		pw.CloseWithError(werr)
	} else {
		werr = pw.Close()
	}

	// The audio is sent, or the attempt is over. Bound the wait for the
	// answer the same way a buffered request is bounded.
	timer := time.AfterFunc(requestTimeout, cancel)
	defer timer.Stop()

	out := <-done
	if out.err != nil {
		return "", out.err
	}
	if werr != nil {
		return "", werr
	}
	return out.text, nil
}

func (c *Client) buffered(ctx context.Context, up Upload) (string, error) {
	data, err := up.Buffered()
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	body.Grow(len(data) + 512)
	mw := multipart.NewWriter(&body)
	err = c.writeForm(mw, up.Filename, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
	if err != nil {
		return "", err
	}

	text, err := c.post(ctx, body.Bytes(), mw.FormDataContentType())
	if err == nil {
		return text, nil
	}
	// A pooled connection the server closed while idle fails on the first
	// write, before the request is seen. One retry pays a fresh handshake
	// instead of surfacing a spurious error to the user.
	var te *transportError
	if errors.As(err, &te) && ctx.Err() == nil {
		return c.post(ctx, body.Bytes(), mw.FormDataContentType())
	}
	return "", err
}

// writeForm writes the multipart request: the small fields first so the
// server has the model selected before the audio arrives, then the audio
// part filled by the caller.
func (c *Client) writeForm(mw *multipart.Writer, filename string, audio func(io.Writer) error) error {
	if err := mw.WriteField("model", c.model()); err != nil {
		return err
	}
	if c.Language != "" {
		if err := mw.WriteField("language", c.Language); err != nil {
			return err
		}
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	if err := audio(part); err != nil {
		return err
	}
	return mw.Close()
}

func (c *Client) setHeaders(req *http.Request, contentType string) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", contentType)
}

type transportError struct{ err error }

func (e *transportError) Error() string { return "http: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("API %d: %s", e.code, e.body) }

func (c *Client) post(ctx context.Context, body []byte, contentType string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transcribeURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.setHeaders(req, contentType)
	return c.do(req)
}

func (c *Client) do(req *http.Request) (string, error) {
	resp, err := c.client().Do(req)
	if err != nil {
		return "", &transportError{err}
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", &statusError{resp.StatusCode, string(b)}
	}

	var result transcribeResponse
	if err := json.Unmarshal(b, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	return result.Text, nil
}
