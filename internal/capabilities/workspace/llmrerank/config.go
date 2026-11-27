// Package llmrerank is an optional second-stage reranker for context_impact.
// It reorders Idiom's top candidates with local scorers (a cross-encoder, an
// embedder fused by reciprocal rank) or a listwise chat model, and falls back
// to Idiom's own order, marked "off", on any failure. It is off by default.
package llmrerank

import (
	"strconv"
	"strings"
	"time"
)

// Backend names, in the order IDIOM_RERANK_BACKENDS lists them.
const (
	BackendCross     = "cross"     // llama.cpp server /v1/rerank (Qwen3-Reranker-0.6B)
	BackendEmbed     = "embed"     // llama.cpp server /v1/embeddings (Qwen3-Embedding-0.6B)
	BackendChat      = "chat"      // OpenAI-compatible chat completions, listwise (local 14B)
	BackendAnthropic = "anthropic" // Anthropic Messages API, listwise (Haiku reference arm)
)

const (
	// ModeOn reranks; ModeDry only estimates tokens and calls nothing.
	ModeOn  = "on"
	ModeDry = "dry"

	defaultPoolSize     = 30
	defaultCrossURL     = "http://127.0.0.1:8181"
	defaultEmbedURL     = "http://127.0.0.1:8182"
	defaultChatURL      = "http://127.0.0.1:8183"
	defaultAnthropicURL = "https://api.anthropic.com"
	defaultAnthropicMod = "claude-haiku-5-5"
	defaultCodeMapPath  = "docs/engineering/code-map.md"
	defaultLocalTimeout = 2 * time.Second
	defaultChatTimeout  = 30 * time.Second
)

// Config is read from the environment at call time. The API key is not part
// of it: the Anthropic backend reads the file KeyFile names on every call.
type Config struct {
	Mode         string
	Backends     []string
	PoolSize     int
	CrossURL     string
	CrossModel   string
	EmbedURL     string
	EmbedModel   string
	ChatURL      string
	ChatModel    string
	AnthropicURL string
	AnthropicMod string
	KeyFile      string
	Timeout      time.Duration // zero: per-backend default
	CacheDir     string
	LogPath      string
	CodeMapPath  string
	Pool         string // queue pool each real call is gated through; empty: ungated
	QueueSocket  string
	AllowRemote  bool
}

// FromEnv builds the config from getenv (os.Getenv in production).
func FromEnv(getenv func(string) string) Config {
	c := Config{
		Mode:         strings.ToLower(strings.TrimSpace(getenv("IDIOM_LLM_RERANK"))),
		PoolSize:     defaultPoolSize,
		CrossURL:     orDefault(getenv("IDIOM_RERANK_CROSS_URL"), defaultCrossURL),
		CrossModel:   orDefault(getenv("IDIOM_RERANK_CROSS_MODEL"), "qwen3-reranker-0.6b"),
		EmbedURL:     orDefault(getenv("IDIOM_RERANK_EMBED_URL"), defaultEmbedURL),
		EmbedModel:   orDefault(getenv("IDIOM_RERANK_EMBED_MODEL"), "qwen3-embedding-0.6b"),
		ChatURL:      orDefault(getenv("IDIOM_RERANK_CHAT_URL"), defaultChatURL),
		ChatModel:    orDefault(getenv("IDIOM_RERANK_CHAT_MODEL"), "qwen3-14b"),
		AnthropicURL: orDefault(getenv("IDIOM_RERANK_ANTHROPIC_URL"), defaultAnthropicURL),
		AnthropicMod: orDefault(getenv("IDIOM_RERANK_ANTHROPIC_MODEL"), defaultAnthropicMod),
		KeyFile:      strings.TrimSpace(getenv("BENTECH_API_KEY_FILE")),
		CacheDir:     strings.TrimSpace(getenv("IDIOM_RERANK_CACHE_DIR")),
		LogPath:      strings.TrimSpace(getenv("IDIOM_RERANK_LOG")),
		CodeMapPath:  orDefault(getenv("IDIOM_RERANK_CODEMAP"), defaultCodeMapPath),
		Pool:         strings.TrimSpace(getenv("IDIOM_RERANK_POOL")),
		QueueSocket:  strings.TrimSpace(getenv("IDIOM_RERANK_QUEUE_SOCKET")),
		AllowRemote:  getenv("IDIOM_RERANK_ALLOW_REMOTE") == "1",
	}

	for _, name := range strings.Split(getenv("IDIOM_RERANK_BACKENDS"), ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			c.Backends = append(c.Backends, name)
		}
	}

	if n, err := strconv.Atoi(strings.TrimSpace(getenv("IDIOM_RERANK_POOL_SIZE"))); err == nil && n > 1 {
		c.PoolSize = n
	}

	if ms, err := strconv.Atoi(strings.TrimSpace(getenv("IDIOM_RERANK_TIMEOUT_MS"))); err == nil && ms > 0 {
		c.Timeout = time.Duration(ms) * time.Millisecond
	}

	return c
}

// Enabled reports whether context_impact should widen its pool and call Run.
func (c Config) Enabled() bool {
	return (c.Mode == ModeOn || c.Mode == ModeDry) && len(c.Backends) > 0
}

func orDefault(v, fallback string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}

	return fallback
}
