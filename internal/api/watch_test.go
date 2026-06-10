package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWatchEventNDJSON(t *testing.T) {
	var buf bytes.Buffer
	ev := watchEvent{Type: "log", Pod: "pod-a", Container: "ray-job-submitter", Line: "hello"}
	if err := json.NewEncoder(&buf).Encode(ev); err != nil {
		t.Fatalf("encode event: %v", err)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("event is not one NDJSON line: %q", buf.String())
	}
}

func TestFormatWatchText(t *testing.T) {
	tests := map[string]watchEvent{
		"::status::RUNNING::ok": {Type: "status", Status: "RUNNING", Message: "ok"},
		"::log::hello":          {Type: "log", Line: "hello"},
		"::error::bad":          {Type: "error", Message: "bad"},
		"::final::FAILED::done": {Type: "final", Status: "FAILED", Message: "done"},
	}
	for want, ev := range tests {
		if got := formatWatchText(ev); got != want {
			t.Fatalf("formatWatchText = %q, want %q", got, want)
		}
	}
}

func TestFinishOnce(t *testing.T) {
	events := make(chan watchEvent, 4)
	finish := finishOnce(context.Background(), events)
	finish(watchEvent{Status: "FAILED"})
	finish(watchEvent{Status: "SUCCEEDED"})

	if got := len(events); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
	ev := <-events
	if ev.Type != "final" || ev.Status != "FAILED" {
		t.Fatalf("event = %#v", ev)
	}
}
