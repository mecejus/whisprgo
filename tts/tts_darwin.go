package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

const speechURL = "https://api.groq.com/openai/v1/audio/speech"

type Client struct {
	APIKey string

	mu     sync.Mutex
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

type speechRequest struct {
	Model          string `json:"model"`
	Voice          string `json:"voice"`
	Input          string `json:"input"`
	ResponseFormat string `json:"response_format"`
}

// Speak synthesizes text via Groq's audio/speech endpoint and plays the WAV
// through afplay. Any prior Speak is superseded: its HTTP request is
// cancelled and its playback killed. Speak blocks until the API responds
// and afplay is launched; playback then continues asynchronously.
func (c *Client) Speak(text string) error {
	if text == "" {
		return nil
	}

	ctx := c.beginSession()

	body, err := json.Marshal(speechRequest{
		Model:          "canopylabs/orpheus-v1-english",
		Voice:          "hannah",
		Input:          text,
		ResponseFormat: "wav",
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", speechURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	wavData, err := io.ReadAll(resp.Body)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API %d: %s", resp.StatusCode, string(wavData))
	}

	return c.play(ctx, wavData)
}

// Stop cancels any in-flight Speak — its HTTP request and/or its afplay
// process — and returns immediately.
func (c *Client) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *Client) beginSession() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	return ctx
}

func (c *Client) stopLocked() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		c.cmd = nil
	}
}

func (c *Client) play(ctx context.Context, wavData []byte) error {
	f, err := os.CreateTemp("", "whisprgo-*.wav")
	if err != nil {
		return err
	}
	if _, err := f.Write(wavData); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	f.Close()

	cmd := exec.Command("afplay", f.Name())

	c.mu.Lock()
	if ctx.Err() != nil {
		c.mu.Unlock()
		os.Remove(f.Name())
		return nil
	}
	if err := cmd.Start(); err != nil {
		c.mu.Unlock()
		os.Remove(f.Name())
		return err
	}
	c.cmd = cmd
	c.mu.Unlock()

	go func(cmd *exec.Cmd, path string) {
		cmd.Wait()
		os.Remove(path)
	}(cmd, f.Name())

	return nil
}
