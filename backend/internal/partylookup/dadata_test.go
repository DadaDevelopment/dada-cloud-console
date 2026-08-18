package partylookup

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSuggestMapsInvoiceRequisites(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Token test-key" {
			t.Fatalf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"count":8,"query":"7807402712","status":["ACTIVE"]}` {
			t.Fatalf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"suggestions":[{"value":"ООО ДАДА","data":{"inn":"7807402712","kpp":"780701001","name":{"full_with_opf":"ООО \"ДАДА\""},"address":{"unrestricted_value":"198335, Санкт-Петербург"}}}]}`))
	}))
	defer server.Close()

	client := newClient("test-key", server.URL, server.Client())
	got, err := client.Suggest(context.Background(), "7807402712")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("suggestions = %#v", got)
	}
	want := Suggestion{INN: "7807402712", KPP: "780701001", Name: `ООО "ДАДА"`, LegalAddress: "198335, Санкт-Петербург"}
	if got[0] != want {
		t.Fatalf("suggestion = %#v, want %#v", got[0], want)
	}
}

func TestSuggestRejectsUpstreamError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	_, err := newClient("test-key", server.URL, server.Client()).Suggest(context.Background(), "780")
	if err == nil {
		t.Fatal("Suggest() error = nil, want error")
	}
}
