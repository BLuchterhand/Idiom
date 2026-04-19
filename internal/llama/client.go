package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const loadingModelRetryDelay = 500 * time.Millisecond

type Client struct {
	baseURL string
	http    *http.Client
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatOptions struct {
	Model             string
	Temperature       float64
	TopP              float64
	TopK              int
	MinP              float64
	PresencePenalty   float64
	RepetitionPenalty float64
	EnableThinking    *bool
	PreserveThinking  bool
	MaxTokens         int
}

type ModelInfo struct {
	ID string `json:"id"`
}

type modelsResponse struct {
	Data []ModelInfo `json:"data"`
}

type chatRequest struct {
	Model              string              `json:"model"`
	Messages           []ChatMessage       `json:"messages"`
	Stream             bool                `json:"stream"`
	Temperature        float64             `json:"temperature"`
	TopP               float64             `json:"top_p"`
	TopK               int                 `json:"top_k,omitempty"`
	MinP               float64             `json:"min_p,omitempty"`
	PresencePenalty    float64             `json:"presence_penalty,omitempty"`
	RepeatPenalty      float64             `json:"repeat_penalty,omitempty"`
	RepetitionPenalty  float64             `json:"repetition_penalty,omitempty"`
	ChatTemplateKwargs *chatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
	MaxTokens          int                 `json:"max_tokens"`
}

type chatTemplateKwargs struct {
	EnableThinking   *bool `json:"enable_thinking,omitempty"`
	PreserveThinking bool  `json:"preserve_thinking,omitempty"`
}

type chatResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type chatStreamChunk struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func buildChatTemplateKwargs(opts ChatOptions) *chatTemplateKwargs {
	if opts.EnableThinking == nil && !opts.PreserveThinking {
		return nil
	}
	return &chatTemplateKwargs{
		EnableThinking:   cloneBoolPtr(opts.EnableThinking),
		PreserveThinking: opts.PreserveThinking,
	}
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, fmt.Errorf("list models failed: %s", strings.TrimSpace(string(body)))
	}

	var parsed modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	return parsed.Data, nil
}

func (c *Client) StreamChat(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error {
	if opts.Model == "" {
		opts.Model = "local-model"
	}

	payload := chatRequest{
		Model:              opts.Model,
		Messages:           messages,
		Stream:             true,
		Temperature:        opts.Temperature,
		TopP:               opts.TopP,
		TopK:               opts.TopK,
		MinP:               opts.MinP,
		PresencePenalty:    opts.PresencePenalty,
		RepeatPenalty:      opts.RepetitionPenalty,
		RepetitionPenalty:  opts.RepetitionPenalty,
		ChatTemplateKwargs: buildChatTemplateKwargs(opts),
		MaxTokens:          opts.MaxTokens,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return fmt.Errorf("chat request failed: %s", strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		chunk := strings.TrimPrefix(line, "data: ")
		if chunk == "[DONE]" {
			return nil
		}

		var parsed chatStreamChunk
		if err := json.Unmarshal([]byte(chunk), &parsed); err != nil {
			return fmt.Errorf("decode stream chunk: %w", err)
		}

		if parsed.Error != nil {
			return fmt.Errorf("%s", parsed.Error.Message)
		}
		if len(parsed.Choices) == 0 {
			continue
		}

		delta := parsed.Choices[0].Delta.Content
		if delta == "" {
			delta = parsed.Choices[0].Message.Content
		}

		if delta != "" {
			if err := onDelta(delta); err != nil {
				return err
			}
		}

		if parsed.Choices[0].FinishReason != nil {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

func (c *Client) CompleteChat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error) {
	if opts.Model == "" {
		opts.Model = "local-model"
	}

	payload := chatRequest{
		Model:              opts.Model,
		Messages:           messages,
		Stream:             false,
		Temperature:        opts.Temperature,
		TopP:               opts.TopP,
		TopK:               opts.TopK,
		MinP:               opts.MinP,
		PresencePenalty:    opts.PresencePenalty,
		RepeatPenalty:      opts.RepetitionPenalty,
		RepetitionPenalty:  opts.RepetitionPenalty,
		ChatTemplateKwargs: buildChatTemplateKwargs(opts),
		MaxTokens:          opts.MaxTokens,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return "", err
		}

		if resp.StatusCode >= http.StatusBadRequest {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
			_ = resp.Body.Close()
			bodyText := strings.TrimSpace(string(body))
			if shouldRetryLoadingModel(resp.StatusCode, bodyText) {
				if err := sleepWithContext(ctx, loadingModelRetryDelay); err != nil {
					return "", err
				}
				continue
			}
			return "", fmt.Errorf("chat request failed: %s", bodyText)
		}

		var parsed chatResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		_ = resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("decode chat response: %w", err)
		}
		if parsed.Error != nil {
			if shouldRetryLoadingModel(http.StatusServiceUnavailable, parsed.Error.Message) {
				if err := sleepWithContext(ctx, loadingModelRetryDelay); err != nil {
					return "", err
				}
				continue
			}
			return "", fmt.Errorf("%s", parsed.Error.Message)
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("chat response did not contain any choices")
		}

		return parsed.Choices[0].Message.Content, nil
	}
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}

func shouldRetryLoadingModel(statusCode int, body string) bool {
	if statusCode != http.StatusServiceUnavailable {
		return false
	}
	body = strings.ToLower(body)
	return strings.Contains(body, "loading model") || strings.Contains(body, "unavailable_error")
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
