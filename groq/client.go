package groq

import (
	"bytes"
	"context"
	"encoding/json"
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

const (
	transcribeURL = "https://api.groq.com/openai/v1/audio/transcriptions"
	modelsURL     = "https://api.groq.com/openai/v1/models"

	transcribeModel = "whisper-large-v3"

	// A transcription that hasn't come back by now is not coming back. Without
	// a bound, a half-open connection wedges the worker until the process dies.
	requestTimeout = 90 * time.Second

	// Re-warming more often than this is pointless; the pooled connection is
	// still good.
	warmInterval = 30 * time.Second
)

type Client struct {
	APIKey string

	once     sync.Once
	http     *http.Client
	lastWarm atomic.Int64
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
		c.http = &http.Client{Transport: t, Timeout: requestTimeout}
	})
	return c.http
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

// Transcribe uploads audio and returns the recognized text. filename's
// extension tells the API the container format (e.g. "audio.flac").
func (c *Client) Transcribe(audio []byte, filename string) (string, error) {
	body, contentType, err := buildForm(audio, filename)
	if err != nil {
		return "", err
	}

	text, err := c.post(body, contentType)
	if err == nil {
		return text, nil
	}
	// A pooled connection the server closed while idle fails on the first
	// write, before the request is seen. One retry pays a fresh handshake
	// instead of surfacing a spurious error to the user.
	if _, isTransport := err.(*transportError); isTransport {
		return c.post(body, contentType)
	}
	return "", err
}

func buildForm(audio []byte, filename string) ([]byte, string, error) {
	var body bytes.Buffer
	body.Grow(len(audio) + 512)
	w := multipart.NewWriter(&body)

	// Small fields first so the server has the model selected before the
	// audio blob arrives.
	if err := w.WriteField("model", transcribeModel); err != nil {
		return nil, "", err
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", "application/octet-stream")
	fw, err := w.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

type transportError struct{ err error }

func (e *transportError) Error() string { return "http: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func (c *Client) post(body []byte, contentType string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, transcribeURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(body))

	resp, err := c.client().Do(req)
	if err != nil {
		return "", &transportError{err}
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API %d: %s", resp.StatusCode, string(b))
	}

	var result transcribeResponse
	if err := json.Unmarshal(b, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	return result.Text, nil
}
