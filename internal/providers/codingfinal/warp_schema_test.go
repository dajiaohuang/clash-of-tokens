package codingfinal

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
)

// Paths are taken from the pinned request.proto, independently of the encoder.
func TestWarpSchemaPaths(t *testing.T) {
	req, err := decodeChatRequest(chatBody())
	if err != nil {
		t.Fatal(err)
	}
	b := mustWarpRequest(t, req, "gpt-5", "conversation-1")
	for _, tc := range []struct {
		path []int
		want string
	}{
		{[]int{2, 6, 1, 1, 1}, "hello"},
		{[]int{3, 1, 1}, "gpt-5"},
		{[]int{3, 1, 2}, "o3"},
		{[]int{3, 1, 3}, "auto"},
		{[]int{4, 1}, "conversation-1"},
	} {
		value := b
		for _, number := range tc.path {
			fields, err := decodeProtoFields(value)
			if err != nil {
				t.Fatalf("path %v: %v", tc.path, err)
			}
			field, ok := firstField(fields, number, 2)
			if !ok {
				t.Fatalf("missing path %v field %d", tc.path, number)
			}
			value = field.bytes
		}
		if string(value) != tc.want {
			t.Fatalf("path %v = %q", tc.path, value)
		}
	}
}

func TestWarpResponseSchemaVectors(t *testing.T) {
	// ResponseEvent.client_actions.actions.{add_messages_to_task.messages,
	// append_to_message_content.message}.agent_output.text, then finished.done.
	for _, vector := range []string{"120b0a091a0712051a030a0178", "120b0a092a070a051a030a0178"} {
		if _, err := hex.DecodeString(vector); err != nil {
			t.Fatal(err)
		}
		got, err := warpToSSE(context.Background(), strings.NewReader("data: "+vector+"\n\ndata: 1a021200\n\n"), "gpt-5")
		if err != nil || !strings.Contains(string(got), `"content":"x"`) {
			t.Fatalf("%s: %s %v", vector, got, err)
		}
	}
}
