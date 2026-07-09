package hibot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 验证非流式 Chat 自动注入 Approve="all"，确保 webchat 单回合不需要人工审批。
func TestChatNonStreamingInjectsApproveAll(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("Action"); got != "Chat" {
			t.Fatalf("Action = %q, want Chat", got)
		}
		if got := r.URL.Query().Get("Version"); got != defaultServerVersion {
			t.Fatalf("Version = %q, want %s", got, defaultServerVersion)
		}
		if got := r.Header.Get("X-Top-Service"); got != defaultServerService {
			t.Fatalf("X-Top-Service = %q, want %s", got, defaultServerService)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeChatSyncResult(w, "ok", nil)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	msg, err := client.V1.Sessions.Chat(context.Background(), "session-1", V1SessionChatParams{
		AgentID: "agent-1",
		Input:   "hello",
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("msg.Content = %q, want ok", msg.Content)
	}
	if got := captured["Approve"]; got != "all" {
		t.Fatalf("Approve = %v, want all", got)
	}
	if got := captured["Stream"]; got != false {
		t.Fatalf("Stream = %v, want false", got)
	}
}

// 验证流式 ChatStreaming 不会自动注入 Approve，保留显式审批语义。
func TestChatStreamingDoesNotInjectApprove(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: completed\ndata: {\"message\":{\"ID\":\"msg-1\"}}\n\n")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	stream := client.V1.Sessions.ChatStreaming(context.Background(), "session-1", V1SessionChatParams{
		AgentID: "agent-1",
		Input:   "hello",
	})
	defer stream.Close()
	for stream.Next() {
	}
	if _, exists := captured["Approve"]; exists {
		t.Fatalf("Approve should not be injected in streaming mode: %#v", captured)
	}
}

// 验证 Chat 支持仅传 Files、空 Content；同时 Files 在 body 中正确序列化。
func TestChatSupportsFilesAndEmptyContent(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeChatSyncResult(w, "ok", []V1MessageFile{{Name: "out.txt", ContentType: "text/plain"}})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	msg, err := client.V1.Sessions.Chat(context.Background(), "session-1", V1SessionChatParams{
		AgentID: "agent-1",
		Files: []V1MessageFile{
			{Name: "report.pdf", ContentType: "application/pdf", BlobID: "blob-123"},
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if got, ok := captured["Content"]; ok && got != "" {
		t.Fatalf("Content = %v, want empty / omitted", got)
	}
	files, ok := captured["Files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("Files missing or wrong type: %#v", captured["Files"])
	}
	first := files[0].(map[string]any)
	if first["Name"] != "report.pdf" || first["ContentType"] != "application/pdf" || first["BlobID"] != "blob-123" {
		t.Fatalf("file payload = %#v", first)
	}
	if msg.Content != "ok" || len(msg.Files) != 1 || msg.Files[0].Name != "out.txt" {
		t.Fatalf("sync message = %#v", msg)
	}
}

func writeChatSyncResult(w http.ResponseWriter, message string, files []V1MessageFile) {
	w.Header().Set("Content-Type", "application/json")
	result := struct {
		Message string          `json:"Message"`
		Files   []V1MessageFile `json:"Files,omitempty"`
	}{Message: message, Files: files}
	b, _ := json.Marshal(result)
	_, _ = fmt.Fprintf(w, `{"ResponseMetadata":{"RequestId":"req-test"},"Result":%s}`, b)
}
