package daemon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseAddMessage(t *testing.T) {
	r := &Response{}

	r.AddMessage("hello", "INFO")
	r.AddMessage("warning", "WARN")

	if len(r.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(r.Messages))
	}
	if r.Messages[0].Message != "hello" || r.Messages[0].Status != "INFO" {
		t.Errorf("First message = %+v, want {hello, INFO}", r.Messages[0])
	}
	if r.Messages[1].Message != "warning" || r.Messages[1].Status != "WARN" {
		t.Errorf("Second message = %+v, want {warning, WARN}", r.Messages[1])
	}
}

func TestResponseAddData(t *testing.T) {
	r := &Response{}

	data := map[string]string{"key": "value"}
	r.AddData(data)

	if r.Data == nil {
		t.Fatal("Expected Data to be set")
	}
}

func TestResponseToJSON(t *testing.T) {
	r := &Response{}
	r.AddMessage("test message", "INFO")
	r.AddData(map[string]string{"key": "value"})

	jsonStr := r.ToJSON()

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("ToJSON() produced invalid JSON: %v", err)
	}

	// Verify structure
	messages, ok := parsed["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("Expected 1 message in JSON, got %v", parsed["messages"])
	}

	if parsed["data"] == nil {
		t.Error("Expected data in JSON output")
	}
}

func TestResponseToJSONEmpty(t *testing.T) {
	r := &Response{}

	jsonStr := r.ToJSON()

	if !strings.Contains(jsonStr, "messages") {
		t.Errorf("Expected 'messages' key in JSON: %s", jsonStr)
	}
}

func TestResponseToJSONOmitsEmptyData(t *testing.T) {
	r := &Response{}
	r.AddMessage("test", "INFO")

	jsonStr := r.ToJSON()

	if strings.Contains(jsonStr, "data") {
		t.Errorf("Expected 'data' to be omitted when nil: %s", jsonStr)
	}
}

func TestStreamingResponseWriteMessage(t *testing.T) {
	var buf bytes.Buffer
	sr := NewStreamingResponse(&buf)

	err := sr.WriteMessage("streaming test", "INFO")
	if err != nil {
		t.Fatalf("WriteMessage() error: %v", err)
	}

	output := buf.String()

	// Should be a JSON line ending with newline
	if !strings.HasSuffix(output, "\n") {
		t.Error("Expected output to end with newline")
	}

	// Parse the JSON
	var msg ResponseMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &msg); err != nil {
		t.Fatalf("Failed to parse streaming message: %v", err)
	}

	if msg.Message != "streaming test" {
		t.Errorf("Expected message %q, got %q", "streaming test", msg.Message)
	}
	if msg.Status != "INFO" {
		t.Errorf("Expected status %q, got %q", "INFO", msg.Status)
	}
}

func TestResponseLogMessages(t *testing.T) {
	// LogMessages calls slog for each message - just ensure it doesn't panic
	r := &Response{}
	r.AddMessage("info message", "INFO")
	r.AddMessage("warn message", "WARN")
	r.AddMessage("error message", "ERROR")
	r.AddMessage("unknown status", "UNKNOWN")

	// Should not panic
	r.LogMessages()
}

func TestStreamingResponseMultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	sr := NewStreamingResponse(&buf)

	sr.WriteMessage("first", "INFO")
	sr.WriteMessage("second", "WARN")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("Expected 2 JSON lines, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var msg ResponseMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Errorf("Line %d is not valid JSON: %v", i, err)
		}
	}
}
