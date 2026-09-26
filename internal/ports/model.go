package ports

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

// ErrUnsupportedInput is a model refusing an attached image or file because
// it cannot read that kind of input. An adapter wraps its provider's own
// rejection in it, since only the adapter can tell that rejection apart from
// any other; the turn then tells the owner the model does not take the
// attachment instead of quoting the provider's error.
var ErrUnsupportedInput = errors.New("model does not accept this input")

type ModelRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	// ReasoningEffort is the level the owner chose, or empty for the
	// provider's own default for the model.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ProviderRouting is the alias's provider-specific routing preference,
	// already in the provider's own wire shape. Only the adapter it was
	// written for understands it; the core passes it through untouched.
	ProviderRouting json.RawMessage `json:"provider_routing,omitempty"`
}

type ModelResponse struct {
	Message Message    `json:"message"`
	Usage   ModelUsage `json:"usage,omitzero"`
	// ReasoningContent is the model's visible chain-of-thought for this
	// response, when the provider returns one. It is never fed back into a
	// following request's message history; the state a provider needs back
	// travels opaquely in Message.ProviderReasoning instead.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type ModelUsage struct {
	PromptTokens       int64 `json:"prompt_tokens"`
	CompletionTokens   int64 `json:"completion_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	CachedPromptTokens int64 `json:"cached_prompt_tokens,omitempty"`
	// CacheWriteTokens is how much of the prompt was written to a cache this
	// call, reported separately by providers that bill cache writes.
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	// CostUSD is what the provider says the call cost, when it says. Zero
	// means unreported, not free.
	CostUSD float64 `json:"cost_usd,omitempty"`
}

func (u ModelUsage) Add(other ModelUsage) ModelUsage {
	return ModelUsage{
		PromptTokens:       u.PromptTokens + other.PromptTokens,
		CompletionTokens:   u.CompletionTokens + other.CompletionTokens,
		TotalTokens:        u.TotalTokens + other.TotalTokens,
		CachedPromptTokens: u.CachedPromptTokens + other.CachedPromptTokens,
		CacheWriteTokens:   u.CacheWriteTokens + other.CacheWriteTokens,
		ReasoningTokens:    u.ReasoningTokens + other.ReasoningTokens,
		CostUSD:            u.CostUSD + other.CostUSD,
	}
}

type Model interface {
	Generate(context.Context, ModelRequest) (ModelResponse, error)
}

// ModelCatalog is the listing side of a model provider: what it says it
// serves. It is deliberately separate from Model rather than folded into it,
// because generating and listing are not the same capability -- a provider
// may serve one model at a fixed URL and have no catalog to offer, and a
// backend that cannot answer this should fail to satisfy the interface rather
// than have to implement a stub. Callers type-assert for it and treat its
// absence as "this provider cannot be browsed", never as an error.
//
// Nothing in the turn path depends on this. It exists so an owner choosing a
// model alias can see what is on offer instead of copying IDs out of a
// vendor's web page; the alias they write is still what governs.
type ModelCatalog interface {
	ListModels(context.Context) ([]CatalogModel, error)
}

// CatalogModel is one entry from a provider's catalog, narrowed to what a
// picker actually shows. ID is the only field a provider must supply -- it is
// what goes in a model alias -- and the rest are hints that providers report
// inconsistently, so every consumer must render them as optional.
type CatalogModel struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	// Reasoning is nil for a model that does not reason at all, or whose
	// provider does not say.
	Reasoning *CatalogReasoning `json:"reasoning,omitempty"`
	// InputModalities is what the provider says the model reads. It is nil
	// whenever the provider does not publish modalities at all, which is the
	// difference between "this model is text-only" and "nobody said". Read it
	// through Accepts rather than directly.
	InputModalities []Modality `json:"input_modalities,omitempty"`
}

// Accepts reports whether the model reads input of one modality, and whether
// that answer is known. A caller that would refuse a part must treat an
// unknown answer as "send it anyway" rather than read silence as a refusal.
func (m CatalogModel) Accepts(modality Modality) (supported, known bool) {
	if m.InputModalities == nil {
		return false, false
	}
	return slices.Contains(m.InputModalities, modality), true
}

// CatalogReasoning is what a provider says about a model's reasoning: the
// effort levels it accepts, and whether it can be told not to reason at all.
// It is what an alias's reasoning_efforts can be filled in from.
type CatalogReasoning struct {
	// Mandatory means the model always reasons and there is no off switch;
	// asking for "none" is either refused or ignored.
	Mandatory bool `json:"mandatory,omitempty"`
	// Efforts is the provider's own list, in the provider's own order. Empty
	// means the model reasons but takes no effort parameter.
	Efforts []string `json:"efforts,omitempty"`
}
