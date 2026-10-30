// Package llm 是模型调用层：只实现 OpenAI 兼容的 /chat/completions，
// 因此 magpie、ollama、vllm、各类中转站都能直接用。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 指向一个 OpenAI 兼容端点。
type Client struct {
	BaseURL     string
	Model       string
	APIKey      string
	Timeout     time.Duration
	MaxTokens   int
	Temperature float64
}

// New 构造客户端；baseURL 末尾的斜杠会去掉，避免拼出 //chat/completions。
func New(baseURL, model, apiKey string, timeout time.Duration, maxTokens int, temperature float64) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Model:       model,
		APIKey:      apiKey,
		Timeout:     timeout,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat 发一次非流式补全，返回助手正文。
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	if c.BaseURL == "" || c.Model == "" {
		return "", errors.New("模型端点未配置")
	}
	payload, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens:   c.MaxTokens,
		Temperature: c.Temperature,
	})
	if err != nil {
		return "", err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用模型失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("模型返回无法解析（HTTP %d）：%s", resp.StatusCode, clip(string(raw), 300))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("模型返回错误：%s", parsed.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("模型返回 HTTP %d：%s", resp.StatusCode, clip(string(raw), 300))
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("模型没有返回任何候选")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("模型返回空正文（可能全是 reasoning，试着调大 max_output_tokens）")
	}
	return content, nil
}

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
