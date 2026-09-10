package routing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"clash-of-tokens/internal/protocol"
)

func TestExecutionStatusDoesNotRetainAdapterSecrets(t *testing.T) {
	r := New(fixture(1))
	lease, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	secret := "https://upstream.test/?api_key=synthetic-private-key response=private-body"
	result := protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamError: secret, UpstreamStatus: 401}
	lease.RecordExecution(result)
	lease.Release(401, 0)
	status := r.Status()
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "synthetic-private") || strings.Contains(string(data), "private-body") || strings.Contains(string(data), "upstream.test") {
		t.Fatal("adapter error text leaked")
	}
	if status[0].LastExecution.UpstreamError != "upstream_error" || status[0].LastExecution.Successful() || status[0].LastExecution.UpstreamStatus != 401 {
		t.Fatal("redaction changed failure semantics")
	}
	if result.UpstreamError != secret {
		t.Fatal("redaction mutated caller result")
	}
	status[0].LastExecution.UpstreamError = secret
	if r.Status()[0].LastExecution.UpstreamError != "upstream_error" {
		t.Fatal("status aliases retained execution")
	}
}
