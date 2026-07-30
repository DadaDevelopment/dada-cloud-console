package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// aiCatalogModel is one model alias a caller may put in the `model` field of an
// OpenAI-compatible request against the gateway.
//
// Alias is the platform-stable name and deliberately NOT the upstream model id:
// the gateway may re-point an alias at a different upstream without breaking
// customer code. Upstream carries that provider-side id for orientation only.
// Kind is "chat" or "embeddings" -- the console uses it to pick the right
// quickstart snippet and to warn when a project has no credential for the
// provider behind the model it just picked.
type aiCatalogModel struct {
	Alias    string `json:"alias"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Upstream string `json:"upstream"`
}

// aiCatalogProvider is one upstream a project can hold a BYOK credential for.
// Label is the human name; KeyURL points at where the customer mints the
// provider key they are about to paste in.
type aiCatalogProvider struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	KeyURL string `json:"key_url"`
}

// aiCatalogProviders and aiCatalogModels mirror the gateway's own routing
// table -- config.yaml `model_list` in the ai-gateway repo plus the
// AI_GW_ALIAS_PROVIDER_JSON env of its argo-infra values. The gateway bakes its
// routes into the image, so there is no runtime endpoint to read them from;
// this list exists to render the console catalog and to reject a credential for
// a provider the gateway cannot route to. Adding a model there means adding it
// here.
var aiCatalogProviders = []aiCatalogProvider{
	{Name: "openai", Label: "OpenAI", KeyURL: "https://platform.openai.com/api-keys"},
	{Name: "anthropic", Label: "Anthropic", KeyURL: "https://console.anthropic.com/settings/keys"},
	{Name: "openrouter", Label: "OpenRouter", KeyURL: "https://openrouter.ai/keys"},
	{Name: "groq", Label: "Groq", KeyURL: "https://console.groq.com/keys"},
	{Name: "sambanova", Label: "SambaNova", KeyURL: "https://cloud.sambanova.ai/apis"},
}

var aiCatalogModels = []aiCatalogModel{
	{Alias: "gpt-4o", Provider: "openai", Kind: "chat", Upstream: "openai/gpt-4o"},
	{Alias: "gpt-4o-mini", Provider: "openai", Kind: "chat", Upstream: "openai/gpt-4o-mini"},
	{Alias: "text-embedding-3-small", Provider: "openai", Kind: "embeddings", Upstream: "openai/text-embedding-3-small"},
	{Alias: "claude", Provider: "anthropic", Kind: "chat", Upstream: "anthropic/claude-sonnet-5"},
	{Alias: "claude-haiku", Provider: "anthropic", Kind: "chat", Upstream: "anthropic/claude-haiku-4-5-20251001"},
	{Alias: "or-gpt-41-mini", Provider: "openrouter", Kind: "chat", Upstream: "openrouter/openai/gpt-4.1-mini"},
	{Alias: "or-gpt-41-mini-online", Provider: "openrouter", Kind: "chat", Upstream: "openrouter/openai/gpt-4.1-mini:online"},
	{Alias: "or-gpt-4o-mini", Provider: "openrouter", Kind: "chat", Upstream: "openrouter/openai/gpt-4o-mini"},
	{Alias: "openrouter-llama", Provider: "openrouter", Kind: "chat", Upstream: "openrouter/meta-llama/llama-3.3-70b-instruct"},
	{Alias: "groq-gpt-oss", Provider: "groq", Kind: "chat", Upstream: "groq/openai/gpt-oss-20b"},
	{Alias: "groq-llama", Provider: "groq", Kind: "chat", Upstream: "groq/llama-3.3-70b-versatile"},
	{Alias: "sambanova-llama", Provider: "sambanova", Kind: "chat", Upstream: "sambanova/Meta-Llama-3.3-70B-Instruct"},
}

// isKnownAIProvider reports whether the gateway can route to this provider at
// all. Storing a credential for anything else would silently never be used.
func isKnownAIProvider(name string) bool {
	for _, p := range aiCatalogProviders {
		if p.Name == name {
			return true
		}
	}
	return false
}

// GetAIGatewayCatalog returns the model aliases and providers the AI Gateway
// can route to. Project-independent: what a project can actually call is this
// catalog intersected with the providers it holds a credential for.
//
// @ID          getAIGatewayCatalog
// @Summary     AI Gateway model and provider catalog
// @Description Lists the model aliases callable through the AI Gateway and the providers a project can store a BYOK credential for. Use the alias (not the upstream model id) as the `model` field of an OpenAI-compatible request.
// @Tags        ai-gateway
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with models, providers and base_url"
// @Failure     401 {object} map[string]string
// @Router      /ai/catalog [get]
func (h *Handler) GetAIGatewayCatalog(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"base_url":  h.aiGatewayBaseURL(),
		"models":    aiCatalogModels,
		"providers": aiCatalogProviders,
	})
}
