package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"
)

const tracedReply = `{"id":"task-2","contextId":"runtime-c1","kind":"task","status":{"state":"completed"},"metadata":{"dada.trace_id":"0af7651916cd43dd8448eb211c80319c","dada.observation_id":"b7ad6b7169203331"},"artifacts":[{"parts":[{"kind":"text","text":"Счёт у FxPro уже есть?"}]}]}`

var _ TracedA2AClient = (*httpA2AClient)(nil)

func TestSendTracedReadsTaskMetadata(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{tracedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	reply, err := newTestA2AClient(srv.URL).SendTraced(context.Background(), testRun())
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "Счёт у FxPro уже есть?" || reply.TraceID != "0af7651916cd43dd8448eb211c80319c" || reply.ObservationID != "b7ad6b7169203331" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestSendTracedWithoutMetadata(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	reply, err := newTestA2AClient(srv.URL).SendTraced(context.Background(), testRun())
	if err != nil {
		t.Fatal(err)
	}
	if reply.TraceID != "" || reply.ObservationID != "" || reply.Text == "" {
		t.Fatalf("reply = %+v", reply)
	}
}
