// Package client — типизированный клиент FinCore API.
//
// Весь код в client.gen.go генерируется из openapi.json (oapi-codegen) и при
// пересборке затирается. Этот файл — единственный написанный руками: он даёт
// конструктор, который требует оба обязательных заголовка.
//
// Почему тенант обязателен: бэкенд мультитенантный, схему БД выбирает заголовок
// x-tenant-slug. Без него запрос уходит в «активное членство» пользователя — то
// есть в непредсказуемый тенант. Спрашиваем слаг один раз, при создании клиента.
//
//	c, err := client.NewFinCoreClient(
//		"https://profi-backend.dada-tuda.ru",
//		os.Getenv("FINCORE_TOKEN"),
//		"dada_development",
//	)
//	if err != nil {
//		return err
//	}
//	resp, err := c.GetSessionTenantWithResponse(ctx)
package client

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// TenantHeader — заголовок выбора тенанта. Зеркалит app.tenant_resolver.TENANT_HEADER.
const TenantHeader = "x-tenant-slug"

// EnvBaseURL, EnvToken и EnvTenant — переменные окружения, из которых берутся
// реквизиты. Токен в код и в репозиторий не кладётся.
const (
	EnvBaseURL = "FINCORE_BASE_URL"
	EnvToken   = "FINCORE_TOKEN"
	EnvTenant  = "FINCORE_TENANT"
)

// DefaultTimeout — таймаут HTTP-клиента по умолчанию.
const DefaultTimeout = 30 * time.Second

// NewFinCoreClient собирает клиент, который шлёт Authorization: Bearer и
// x-tenant-slug на каждом запросе. Пустой baseURL, token или tenantSlug —
// ошибка, а не молчаливый запрос не в тот тенант.
func NewFinCoreClient(baseURL, token, tenantSlug string, opts ...ClientOption) (*ClientWithResponses, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("fincore: baseURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("fincore: token is required")
	}
	if tenantSlug == "" {
		return nil, fmt.Errorf(
			"fincore: tenantSlug is required: without the %s header the request "+
				"silently resolves to the user's default tenant", TenantHeader)
	}

	base := []ClientOption{
		WithHTTPClient(&http.Client{Timeout: DefaultTimeout}),
		WithRequestEditorFn(authEditor(token, tenantSlug)),
	}

	return NewClientWithResponses(strings.TrimRight(baseURL, "/"), append(base, opts...)...)
}

// NewFinCoreClientFromEnv — то же, но реквизиты берутся из FINCORE_BASE_URL,
// FINCORE_TOKEN и FINCORE_TENANT.
func NewFinCoreClientFromEnv(opts ...ClientOption) (*ClientWithResponses, error) {
	var missing []string
	for _, name := range []string{EnvBaseURL, EnvToken, EnvTenant} {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("fincore: missing environment variables: %s", strings.Join(missing, ", "))
	}

	return NewFinCoreClient(os.Getenv(EnvBaseURL), os.Getenv(EnvToken), os.Getenv(EnvTenant), opts...)
}

// authEditor проставляет оба обязательных заголовка перед отправкой.
func authEditor(token, tenantSlug string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(TenantHeader, tenantSlug)
		return nil
	}
}
