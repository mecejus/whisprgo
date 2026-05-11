package groq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const chatURL = "https://api.groq.com/openai/v1/chat/completions"

const systemPrompt = "You are an assistant that gives very brief answers to the questions with minimal amount of words. Express all information using only plain words and standard punctuation, completely avoiding technical symbols, abbreviations, or special characters. Omit all citation markers, source references, and bracketed line pointers from your responses."

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatTool struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Messages            []chatMessage `json:"messages"`
	Model               string        `json:"model"`
	Temperature         float64       `json:"temperature"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
	TopP                float64       `json:"top_p"`
	Stream              bool          `json:"stream"`
	ReasoningEffort     string        `json:"reasoning_effort"`
	Tools               []chatTool    `json:"tools"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Ask sends a single user turn to the chat completions endpoint and returns
// the assistant's reply. Each call is independent — no conversation memory is
// carried across invocations.
func (c *Client) Ask(prompt string) (string, error) {
	reqBody := chatRequest{
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Model:               "openai/gpt-oss-120b",
		Temperature:         1,
		MaxCompletionTokens: 300,
		TopP:                1,
		Stream:              false,
		ReasoningEffort:     "low",
		Tools:               []chatTool{{Type: "browser_search"}},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", chatURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API %d: %s", resp.StatusCode, string(b))
	}

	var out chatResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return out.Choices[0].Message.Content, nil
}
