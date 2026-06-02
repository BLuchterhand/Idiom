package llama

import "errors"

// ErrAPIError is returned when the LLM backend responds with an
// error payload instead of a successful completion or chat message.
var ErrAPIError = errors.New("llm api returned an error")
