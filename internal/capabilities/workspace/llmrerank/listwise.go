package llmrerank

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	listwiseMaxTokens = 256
	listwiseSystem    = "You rank source files for a developer's search query. Reply with only a JSON array of the candidate numbers, most relevant first, for example [3,1,2]. No other text."
	noThinkSuffix     = "\n/no_think" // Qwen3's switch to skip its reasoning block
	defaultKeyFile    = ".config/anthropic/bentech.key"
)

var (
	thinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)
	jsonArray  = regexp.MustCompile(`\[[\d,\s]*\]`)
)

// listwisePrompt numbers the candidates from 1 (RankGPT style: one call, the
// whole list in, a permutation out).
func listwisePrompt(query string, docs []Doc) string {
	var b strings.Builder

	b.WriteString("Query: " + query + "\n\nCandidates:\n")

	for i, d := range docs {
		fmt.Fprintf(&b, "%d. %s\n", i+1, d.Text)
	}

	b.WriteString("\nReturn the JSON array of candidate numbers, most relevant first.")

	return b.String()
}

// parseListwise reads the last JSON array of 1-based numbers in text and
// completes it to a full permutation; no usable array is an error.
func parseListwise(text string, n int) ([]int, error) {
	text = thinkBlock.ReplaceAllString(text, "")
	matches := jsonArray.FindAllString(text, -1)

	for k := len(matches) - 1; k >= 0; k-- {
		var nums []int
		if json.Unmarshal([]byte(matches[k]), &nums) != nil {
			continue
		}

		zero := make([]int, len(nums))
		for i, v := range nums {
			zero[i] = v - 1
		}

		if order, ok := completeOrder(zero, n); ok {
			return order, nil
		}
	}

	return nil, fmt.Errorf("no index list in reply")
}

func estimateListwise(query string, docs []Doc) (int, int) {
	prompt := listwisePrompt(query, docs)

	return estimateTokens(len(listwiseSystem) + len(prompt)), 2*len(docs) + 4
}

// chatBackend asks an OpenAI-compatible chat endpoint (mlx_lm.server,
// llama.cpp, ollama) for a listwise order.
type chatBackend struct{ url, model string }

func (b chatBackend) Name() string { return BackendChat }

func (b chatBackend) Estimate(q string, docs []Doc) (int, int) { return estimateListwise(q, docs) }

func (b chatBackend) Rank(ctx context.Context, query string, docs []Doc) (Ranked, error) {
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}

	body := map[string]any{
		"model":       b.model,
		"temperature": 0,
		"max_tokens":  listwiseMaxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": listwiseSystem},
			{"role": "user", "content": listwisePrompt(query, docs) + noThinkSuffix},
		},
	}

	if err := postJSON(ctx, strings.TrimRight(b.url, "/")+"/v1/chat/completions", nil, body, &reply); err != nil {
		return Ranked{}, err
	}

	if len(reply.Choices) == 0 {
		return Ranked{}, fmt.Errorf("no choices")
	}

	order, err := parseListwise(reply.Choices[0].Message.Content, len(docs))
	if err != nil {
		return Ranked{}, err
	}

	return Ranked{Order: order, InTokens: reply.Usage.Prompt, OutTokens: reply.Usage.Completion}, nil
}

// anthropicBackend is the Haiku reference arm: the same listwise prompt over
// the Messages API. The key is read from the file at call time and appears
// only in the x-api-key header.
type anthropicBackend struct{ url, model, keyFile string }

func (b anthropicBackend) Name() string { return BackendAnthropic }

func (b anthropicBackend) Estimate(q string, docs []Doc) (int, int) { return estimateListwise(q, docs) }

func (b anthropicBackend) Rank(ctx context.Context, query string, docs []Doc) (Ranked, error) {
	key, err := readKey(b.keyFile)
	if err != nil {
		return Ranked{}, err
	}

	var reply struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}

	body := map[string]any{
		"model":      b.model,
		"max_tokens": listwiseMaxTokens,
		"system":     listwiseSystem,
		"thinking":   map[string]string{"type": "disabled"},
		"messages":   []map[string]string{{"role": "user", "content": listwisePrompt(query, docs)}},
	}
	headers := map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}

	if err := postJSON(ctx, strings.TrimRight(b.url, "/")+"/v1/messages", headers, body, &reply); err != nil {
		return Ranked{}, err
	}

	var text strings.Builder

	for _, c := range reply.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}

	order, err := parseListwise(text.String(), len(docs))
	if err != nil {
		return Ranked{}, err
	}

	return Ranked{Order: order, InTokens: reply.Usage.In, OutTokens: reply.Usage.Out}, nil
}

// readKey reads the key file named by BENTECH_API_KEY_FILE, else the default
// under the home directory. Errors never include the path or contents.
func readKey(file string) (string, error) {
	if file == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no api key file")
		}

		file = filepath.Join(home, defaultKeyFile)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("api key file unreadable")
	}

	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("api key file empty")
	}

	return key, nil
}
