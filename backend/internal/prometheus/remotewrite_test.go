package prometheus

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/snappy"
	"google.golang.org/protobuf/encoding/protowire"
)

// decoded mirrors of the remote-write messages, rebuilt from the wire bytes so
// the test proves the hand-rolled encoder produces parseable protobuf.
type decTS struct {
	labels map[string]string
	value  float64
	tsMS   int64
}

func decodeWriteRequest(t *testing.T, b []byte) []decTS {
	t.Helper()
	var out []decTS
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType {
			t.Fatalf("WriteRequest: unexpected field %d type %d", num, typ)
		}
		msg, n := protowire.ConsumeBytes(b)
		if n < 0 {
			t.Fatalf("bad bytes: %v", protowire.ParseError(n))
		}
		b = b[n:]
		out = append(out, decodeTimeSeries(t, msg))
	}
	return out
}

func decodeTimeSeries(t *testing.T, b []byte) decTS {
	t.Helper()
	ts := decTS{labels: map[string]string{}}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		b = b[n:]
		sub, n := protowire.ConsumeBytes(b)
		if n < 0 {
			t.Fatalf("bad bytes: %v", protowire.ParseError(n))
		}
		b = b[n:]
		switch num {
		case 1: // Label
			name, value := decodeLabel(t, sub)
			ts.labels[name] = value
		case 2: // Sample
			ts.value, ts.tsMS = decodeSample(t, sub)
		default:
			t.Fatalf("TimeSeries: unexpected field %d type %d", num, typ)
		}
	}
	return ts
}

func decodeLabel(t *testing.T, b []byte) (string, string) {
	t.Helper()
	var name, value string
	for len(b) > 0 {
		num, _, n := protowire.ConsumeTag(b)
		b = b[n:]
		s, n := protowire.ConsumeString(b)
		if n < 0 {
			t.Fatalf("bad label string: %v", protowire.ParseError(n))
		}
		b = b[n:]
		switch num {
		case 1:
			name = s
		case 2:
			value = s
		}
	}
	return name, value
}

func decodeSample(t *testing.T, b []byte) (float64, int64) {
	t.Helper()
	var v float64
	var ms int64
	for len(b) > 0 {
		num, _, n := protowire.ConsumeTag(b)
		b = b[n:]
		switch num {
		case 1:
			bits, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				t.Fatalf("bad fixed64: %v", protowire.ParseError(n))
			}
			b = b[n:]
			v = math.Float64frombits(bits)
		case 2:
			raw, n := protowire.ConsumeVarint(b)
			if n < 0 {
				t.Fatalf("bad varint: %v", protowire.ParseError(n))
			}
			b = b[n:]
			ms = int64(raw)
		}
	}
	return v, ms
}

func TestMarshalWriteRequest_RoundTrip(t *testing.T) {
	in := []WriteSeries{
		{
			Labels:      map[string]string{"__name__": "http_requests_total", "org_id": "org1", "source": "api"},
			Value:       42.5,
			TimestampMS: 1700000000000,
		},
		{
			Labels:      map[string]string{"__name__": "cpu_seconds", "project_id": "p2"},
			Value:       0.001,
			TimestampMS: 1700000001000,
		},
	}
	got := decodeWriteRequest(t, marshalWriteRequest(in))
	if len(got) != len(in) {
		t.Fatalf("series count: got %d want %d", len(got), len(in))
	}
	for i := range in {
		if got[i].value != in[i].Value {
			t.Errorf("series %d value: got %v want %v", i, got[i].value, in[i].Value)
		}
		if got[i].tsMS != in[i].TimestampMS {
			t.Errorf("series %d ts: got %v want %v", i, got[i].tsMS, in[i].TimestampMS)
		}
		for k, v := range in[i].Labels {
			if got[i].labels[k] != v {
				t.Errorf("series %d label %q: got %q want %q", i, k, got[i].labels[k], v)
			}
		}
	}
}

func TestWriteClient_PostsSnappyProtobuf(t *testing.T) {
	var gotBody []decTS
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		raw := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			raw = append(raw, buf[:n]...)
			if err != nil {
				break
			}
		}
		decoded, err := snappy.Decode(nil, raw)
		if err != nil {
			t.Errorf("snappy decode: %v", err)
		}
		gotBody = decodeWriteRequest(t, decoded)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewWriteClient(srv.URL, "u", "p")
	if c == nil {
		t.Fatal("NewWriteClient returned nil for non-empty URL")
	}
	err := c.Write(context.Background(), "org-7", []WriteSeries{
		{Labels: map[string]string{"__name__": "m", "org_id": "o"}, Value: 1, TimestampMS: 5},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotHeaders.Get("X-Scope-OrgID") != "org-7" {
		t.Errorf("X-Scope-OrgID: got %q, want org-7", gotHeaders.Get("X-Scope-OrgID"))
	}
	if gotHeaders.Get("Content-Encoding") != "snappy" {
		t.Errorf("Content-Encoding: got %q", gotHeaders.Get("Content-Encoding"))
	}
	if gotHeaders.Get("Content-Type") != "application/x-protobuf" {
		t.Errorf("Content-Type: got %q", gotHeaders.Get("Content-Type"))
	}
	if gotHeaders.Get("Authorization") == "" {
		t.Error("expected basic auth header")
	}
	if len(gotBody) != 1 || gotBody[0].labels["__name__"] != "m" {
		t.Errorf("decoded body wrong: %+v", gotBody)
	}
}

func TestNewWriteClient_EmptyURLReturnsNil(t *testing.T) {
	if NewWriteClient("", "", "") != nil {
		t.Error("expected nil for empty URL")
	}
}

func TestNewWriteClient_AppendsWritePath(t *testing.T) {
	c := NewWriteClient("https://prom.example.com", "", "")
	if c.endpoint != "https://prom.example.com/api/v1/write" {
		t.Errorf("endpoint: got %q", c.endpoint)
	}
	c2 := NewWriteClient("https://prom.example.com/api/v1/write", "", "")
	if c2.endpoint != "https://prom.example.com/api/v1/write" {
		t.Errorf("endpoint not deduped: got %q", c2.endpoint)
	}
	// Grafana Mimir's remote-write path is /api/v1/push — must be used as-is,
	// not have /api/v1/write appended onto it.
	c3 := NewWriteClient("http://mimir:8080/api/v1/push", "", "")
	if c3.endpoint != "http://mimir:8080/api/v1/push" {
		t.Errorf("mimir push endpoint mangled: got %q", c3.endpoint)
	}
}
