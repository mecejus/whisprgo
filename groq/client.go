package groq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

const transcribeURL = "https://api.groq.com/openai/v1/audio/transcriptions"

type Client struct {
	APIKey string
}

type transcribeResponse struct {
	Text string `json:"text"`
}

func (c *Client) Transcribe(wavData []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	h.Set("Content-Type", "audio/wav")
	fw, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(wavData); err != nil {
		return "", err
	}

	if err := w.WriteField("model", "whisper-large-v3"); err != nil {
		return "", err
	}
	w.Close()

	req, err := http.NewRequest("POST", transcribeURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
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
