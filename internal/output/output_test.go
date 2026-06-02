package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestEnvelopeShape is the load-bearing contract test for the JSON output.
// The skill and any subagent code that branches on _meta.crocs depends on
// this exact shape — when it changes, bump SchemaVersion and update
// MIGRATION.md alongside the breaking change.
func TestEnvelopeShape(t *testing.T) {
	type payload struct {
		Projects []string `json:"projects"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, "list", payload{Projects: []string{"a", "b"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}

	meta, ok := got["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta missing or wrong type; got %T", got["_meta"])
	}
	if v := meta["crocs"]; v != "1" {
		t.Errorf("_meta.crocs = %v, want \"1\"", v)
	}
	if v := meta["command"]; v != "list" {
		t.Errorf("_meta.command = %v, want \"list\"", v)
	}

	projects, ok := got["projects"].([]any)
	if !ok {
		t.Fatalf("projects missing or wrong type; got %T", got["projects"])
	}
	if len(projects) != 2 {
		t.Errorf("projects has %d entries, want 2", len(projects))
	}
}

// TestEmptyPayload — when the payload has no exported fields, the envelope
// still emits the _meta object alone.
func TestEmptyPayload(t *testing.T) {
	type empty struct{}
	var buf bytes.Buffer
	if err := Write(&buf, "ping", empty{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if _, ok := got["_meta"]; !ok {
		t.Errorf("_meta missing on empty payload")
	}
	if len(got) != 1 {
		t.Errorf("expected exactly _meta in output, got %d keys: %v", len(got), got)
	}
}

// TestRejectsNonObjectPayload — slices and primitives at the top level break
// the envelope-merge invariant; we surface that loudly.
func TestRejectsNonObjectPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "x", []string{"a"}); err == nil {
		t.Fatal("Write of slice payload should error; got no error")
	}
	if err := Write(&buf, "x", "hello"); err == nil {
		t.Fatal("Write of string payload should error; got no error")
	}
}

// TestHTMLEscaping — ensure URLs and angle brackets pass through without
// `<` mangling. Agent consumers parse the JSON directly, not in a
// browser, so HTML escaping costs readability with zero benefit.
func TestHTMLEscaping(t *testing.T) {
	type payload struct {
		URL string `json:"url"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, "info", payload{URL: "https://example.com/path?x=1&y=<2>"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.Contains(buf.String(), "\\u003c") {
		t.Errorf("URL was HTML-escaped:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "x=1&y=<2>") {
		t.Errorf("URL not emitted verbatim:\n%s", buf.String())
	}
}
