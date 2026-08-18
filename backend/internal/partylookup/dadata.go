// Package partylookup resolves Russian legal-entity requisites from an INN.
package partylookup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const dadataSuggestPartyURL = "https://suggestions.dadata.ru/suggestions/api/4_1/rs/suggest/party"

// Suggestion is the invoice-relevant slice of a DaData company result.
type Suggestion struct {
	INN          string `json:"inn"`
	KPP          string `json:"kpp,omitempty"`
	Name         string `json:"name"`
	LegalAddress string `json:"legal_address"`
}

// Client calls DaData's organisation-suggestion API. It deliberately exposes
// only the requisites the invoice form needs, never DaData's whole response.
type Client struct {
	token      string
	endpoint   string
	httpClient *http.Client
}

// New creates a DaData organisation client.
func New(token string) *Client {
	return newClient(token, dadataSuggestPartyURL, &http.Client{Timeout: 5 * time.Second})
}

func newClient(token, endpoint string, httpClient *http.Client) *Client {
	return &Client{token: token, endpoint: endpoint, httpClient: httpClient}
}

// Suggest returns active legal entities and individual entrepreneurs matching
// the supplied partial or complete INN. DaData supplies each result's name,
// KPP and legal address in the same response.
func (c *Client) Suggest(ctx context.Context, query string) ([]Suggestion, error) {
	body, err := json.Marshal(map[string]any{
		"query":  query,
		"count":  8,
		"status": []string{"ACTIVE"},
	})
	if err != nil {
		return nil, fmt.Errorf("dadata: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("dadata: build request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dadata: call suggest party: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("dadata: unexpected status %d", resp.StatusCode)
	}

	var payload struct {
		Suggestions []struct {
			Value string `json:"value"`
			Data  struct {
				INN  string `json:"inn"`
				KPP  string `json:"kpp"`
				Name struct {
					FullWithOPF string `json:"full_with_opf"`
				} `json:"name"`
				Address struct {
					Value             string `json:"value"`
					UnrestrictedValue string `json:"unrestricted_value"`
				} `json:"address"`
			} `json:"data"`
		} `json:"suggestions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("dadata: decode response: %w", err)
	}

	result := make([]Suggestion, 0, len(payload.Suggestions))
	for _, item := range payload.Suggestions {
		name := strings.TrimSpace(item.Data.Name.FullWithOPF)
		if name == "" {
			name = strings.TrimSpace(item.Value)
		}
		address := strings.TrimSpace(item.Data.Address.UnrestrictedValue)
		if address == "" {
			address = strings.TrimSpace(item.Data.Address.Value)
		}
		if item.Data.INN == "" || name == "" || address == "" {
			continue
		}
		result = append(result, Suggestion{
			INN:          item.Data.INN,
			KPP:          item.Data.KPP,
			Name:         name,
			LegalAddress: address,
		})
	}
	return result, nil
}
