package llama

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/platform/config"
)

const modelCapabilityProfileSchemaVersion = "idiom.model_capability_profile.v1"

// CapabilityStatus distinguishes a known limitation from a capability that
// the adapter has not established. Unknown must not be treated as unsupported
// when selecting optional request features.
type CapabilityStatus string

const (
	CapabilitySupported   CapabilityStatus = "supported"
	CapabilityUnsupported CapabilityStatus = "unsupported"
	CapabilityUnknown     CapabilityStatus = "unknown"
)

type CapabilityLimit struct {
	Value  int              `json:"value,omitempty"`
	Status CapabilityStatus `json:"status"`
}

type ChatTemplateProfile struct {
	SystemMessagesMustLead CapabilityStatus `json:"system_messages_must_lead"`
}

// ModelCapabilityProfile is the immutable, metadata-only contract used by a
// run to select model request behavior.
type ModelCapabilityProfile struct {
	SchemaVersion string `json:"schema_version"`
	Backend       string `json:"backend"`
	Protocol      string `json:"protocol"`
	Model         string `json:"model,omitempty"`
	Source        string `json:"source"`

	StructuredOutput StructuredOutputCapabilities `json:"structured_output"`
	ContextWindow    CapabilityLimit              `json:"context_window"`
	MaxOutputTokens  CapabilityLimit              `json:"max_output_tokens"`
	ChatTemplate     ChatTemplateProfile          `json:"chat_template"`
	PromptCache      CapabilityStatus             `json:"prompt_cache"`
	Vision           CapabilityStatus             `json:"vision"`
	ServerLifecycle  CapabilityStatus             `json:"server_lifecycle"`

	Fingerprint string `json:"fingerprint"`
}

func (p ModelCapabilityProfile) Validate() error {
	if p.SchemaVersion != modelCapabilityProfileSchemaVersion {
		return fmt.Errorf("unsupported model capability profile schema %q", p.SchemaVersion)
	}
	if strings.TrimSpace(p.Backend) == "" || strings.TrimSpace(p.Protocol) == "" {
		return fmt.Errorf("model capability profile requires backend and protocol")
	}
	if p.ContextWindow.Status == CapabilitySupported && p.ContextWindow.Value <= 0 {
		return fmt.Errorf("supported context window must be positive")
	}
	if p.MaxOutputTokens.Status == CapabilitySupported && p.MaxOutputTokens.Value <= 0 {
		return fmt.Errorf("supported max output tokens must be positive")
	}
	return nil
}

func (p ModelCapabilityProfile) WithFingerprint() ModelCapabilityProfile {
	p.Fingerprint = ""
	data, err := json.Marshal(p)
	if err != nil {
		return p
	}
	hash := sha256.Sum256(data)
	p.Fingerprint = hex.EncodeToString(hash[:])
	return p
}

func (p ModelCapabilityProfile) EffectiveContextLimit(configured int) int {
	if configured <= 0 || p.ContextWindow.Status != CapabilitySupported {
		return configured
	}
	if configured > p.ContextWindow.Value {
		return p.ContextWindow.Value
	}
	return configured
}

// BackendModelCapabilityProfile projects adapter facts into the stable model
// contract. Limits remain unknown unless the backend has supplied them; the
// runtime must not invent a model context window from a provider name.
func BackendModelCapabilityProfile(backend, model string) ModelCapabilityProfile {
	adapter := backendAdapterFor(backend)
	profile := ModelCapabilityProfile{
		SchemaVersion:    modelCapabilityProfileSchemaVersion,
		Backend:          adapter.Kind(),
		Protocol:         string(adapter.Protocol()),
		Model:            strings.TrimSpace(model),
		Source:           "configured_default",
		StructuredOutput: adapter.StructuredOutputCapabilities(),
		ContextWindow:    CapabilityLimit{Status: CapabilityUnknown},
		MaxOutputTokens:  CapabilityLimit{Status: CapabilityUnknown},
		ChatTemplate: ChatTemplateProfile{
			SystemMessagesMustLead: capabilityStatusForBackend(adapter.Kind() == config.BackendMLX),
		},
		PromptCache:     CapabilityUnknown,
		Vision:          CapabilityUnknown,
		ServerLifecycle: CapabilityUnknown,
	}
	return profile.WithFingerprint()
}

func capabilityStatusForBackend(supported bool) CapabilityStatus {
	if supported {
		return CapabilitySupported
	}
	return CapabilityUnknown
}

func (c *Client) CapabilityProfile() ModelCapabilityProfile {
	if c == nil {
		return BackendModelCapabilityProfile("", "")
	}
	return BackendModelCapabilityProfile(c.Backend(), c.modelName)
}

// DiscoverCapabilityProfile validates the configured model against the
// backend's model endpoint and records that the identity was observed there.
// The endpoint currently standardizes identity only; optional capability
// limits remain unknown until a backend exposes them through a stable schema.
func (c *Client) DiscoverCapabilityProfile(ctx context.Context, model string) (ModelCapabilityProfile, error) {
	profile := BackendModelCapabilityProfile(c.Backend(), model)
	models, err := c.ListModels(ctx)
	if err != nil {
		return profile, err
	}
	wanted := strings.TrimSpace(model)
	if wanted == "" {
		wanted = strings.TrimSpace(c.modelName)
	}
	if wanted != "" {
		found := false
		for _, candidate := range models {
			if strings.TrimSpace(candidate.ID) == wanted {
				found = true
				break
			}
		}
		if !found {
			return profile, fmt.Errorf("model %q was not advertised by backend", wanted)
		}
	}
	profile.Source = "models_endpoint"
	return profile.WithFingerprint(), profile.Validate()
}
