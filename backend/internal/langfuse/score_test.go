package langfuse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type scoreServer struct {
	mu        sync.Mutex
	scoreCode int
	singles   []Score
	rateLimit int
}

func (s *scoreServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case scorePath:
			var sc Score
			_ = json.Unmarshal(raw, &sc)
			if s.rateLimit > 0 {
				s.rateLimit--
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"message":"Rate limit exceeded","details":{"retryAfterSeconds":0}}`))
				return
			}
			s.singles = append(s.singles, sc)
			w.WriteHeader(s.scoreCode)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func scoreClient(srv *httptest.Server) *Client {
	c := New(srv.URL, "pk", "sk", true)
	c.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	return c
}

func TestCreateScoresPostsEachScore(t *testing.T) {
	s := &scoreServer{scoreCode: http.StatusOK}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	scores := []Score{
		{TraceID: "t1", ObservationID: "o1", Name: "turn.total", Value: 100, DataType: ScoreNumeric},
		{TraceID: "t1", ObservationID: "o1", Name: "turn.bad_form", Value: 0, DataType: ScoreBoolean},
	}
	if err := scoreClient(srv).CreateScores(context.Background(), scores); err != nil {
		t.Fatal(err)
	}
	if len(s.singles) != 2 || s.singles[1].Name != "turn.bad_form" || s.singles[1].DataType != ScoreBoolean || s.singles[1].ObservationID != "o1" {
		t.Fatalf("singles = %+v", s.singles)
	}
}

func TestCreateScoreWaitsOutRateLimit(t *testing.T) {
	s := &scoreServer{scoreCode: http.StatusOK, rateLimit: 2}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	if err := scoreClient(srv).CreateScore(context.Background(), Score{TraceID: "t1", Name: "a", Value: 1}); err != nil {
		t.Fatal(err)
	}
	if len(s.singles) != 1 {
		t.Fatalf("singles = %+v", s.singles)
	}
}

func TestCreateScoreGivesUpOnCancelledContext(t *testing.T) {
	s := &scoreServer{scoreCode: http.StatusOK, rateLimit: 10}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := scoreClient(srv).CreateScore(ctx, Score{TraceID: "t1", Name: "a", Value: 1})
	if err == nil {
		t.Fatal("expected an error")
	}
}
