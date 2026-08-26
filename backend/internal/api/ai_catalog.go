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
//
// The tier rows named gemini as a failover partner until 2026-08-04 and it was
// never a member: gemini answers 400 FAILED_PRECONDITION "user location is not
// supported" to every request from this cluster, so the catalog was promising a
// provider diversity the tiers did not have. Their Upstream strings now name
// the providers each group actually holds members on.
// aiCatalogPlatformProvider marks the tier aliases the gateway serves from the
// platform's own keys (migration 079). They are deliberately absent from
// aiCatalogProviders: a project can never store a BYOK credential for them, and
// the console must not tell anyone they need one.
const aiCatalogPlatformProvider = "platform"

// aiPlatformOwnedProvider is the upstream behind every tier and behind vision:
// the one provider the platform holds its own key for, so a project that
// configured nothing is still served (migration 079). It is not in
// aiCatalogProviders on purpose -- that list answers "which provider may a
// customer store a key for", and this one they may not.
const aiPlatformOwnedProvider = "nvidia_nim"

var aiCatalogProviders = []aiCatalogProvider{
	{Name: "openai", Label: "OpenAI", KeyURL: "https://platform.openai.com/api-keys"},
	{Name: "anthropic", Label: "Anthropic", KeyURL: "https://console.anthropic.com/settings/keys"},
	{Name: "openrouter", Label: "OpenRouter", KeyURL: "https://openrouter.ai/keys"},
	{Name: "groq", Label: "Groq", KeyURL: "https://console.groq.com/keys"},
	{Name: "sambanova", Label: "SambaNova", KeyURL: "https://cloud.sambanova.ai/apis"},
	{Name: "sotamodel", Label: "SotaModel", KeyURL: ""},
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
	{Alias: "fast", Provider: aiCatalogPlatformProvider, Kind: "chat", Upstream: "tier alias, fails over across nvidia_nim (x2) and groq"},
	{Alias: "medium", Provider: aiCatalogPlatformProvider, Kind: "chat", Upstream: "tier alias, fails over across nvidia_nim (x2) and sambanova"},
	{Alias: "smart", Provider: aiCatalogPlatformProvider, Kind: "chat", Upstream: "tier alias, fails over across nvidia_nim (x3)"},
	{Alias: "vision", Provider: aiCatalogPlatformProvider, Kind: "chat", Upstream: "image input, nvidia_nim (x2) -- no other reachable provider reads the image"},
	{Alias: "search", Provider: "groq", Kind: "chat", Upstream: "groq/groq/compound-mini, answer grounded in a web search groq runs itself; rejects requests carrying tools"},
}

// isKnownAIAlias reports whether the gateway serves this model group.
//
// Used to bound the label set of the upstream-failure metrics: the gateway is
// the only caller and is guarded by the internal token, but a metric label fed
// from a request body is a cardinality bomb one bad deploy away, and an
// unbounded series is how a monitoring stack falls over during the outage it
// was meant to report.
func isKnownAIAlias(name string) bool {
	for _, m := range aiCatalogModels {
		if m.Alias == name {
			return true
		}
	}
	return false
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
