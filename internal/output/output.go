// Package output owns the stable JSON envelope every crocs/crocs command
// emits. PLAN.md §1 Breaking Change #1: top-level "_meta" with the schema
// version key "crocs" and the invoking command name.
//
//	{
//	  "_meta": {"crocs": "1", "command": "list"},
//	  "projects": [...]
//	}
//
// `read-files` is the sole carve-out (XML envelope, handled in internal/files).
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// SchemaVersion is the value of "_meta.crocs". Bump only on contract-breaking
// changes — that's a Breaking Change in MIGRATION.md.
const SchemaVersion = "1"

// MetaKey is the top-level envelope field name. Lifted to a constant so the
// eventual rename from crocs to crocs is a one-line change.
const MetaKey = "crocs"

// Meta is the envelope's `_meta` value.
type Meta struct {
	// Crocs holds the schema-version string. JSON-tagged "crocs" per PLAN.md;
	// consumers branch on this. (Field name is intentionally the eventual
	// tool name, not the binary name.)
	Crocs   string `json:"crocs"`
	Command string `json:"command"`
}

// envelope wraps a payload so the rendered JSON has _meta at the top level
// next to the payload's own fields (rather than nested under "data").
type envelope struct {
	Meta    Meta
	Payload any
}

// MarshalJSON serializes the envelope by first marshaling the payload, then
// the meta, then merging both at the top level. The payload must marshal to a
// JSON object — anything else (array, primitive) is an API design bug at the
// call site, so we surface it explicitly.
//
// Both halves are emitted with HTML escaping OFF so that URLs containing
// `&`, `<`, or `>` survive intact — agents parse the JSON raw, not in a
// browser. Standard json.Marshal would turn those into & / <.
func (e envelope) MarshalJSON() ([]byte, error) {
	payloadBytes, err := marshalNoEscape(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	if len(payloadBytes) == 0 || payloadBytes[0] != '{' {
		return nil, fmt.Errorf("output: payload must marshal to a JSON object, got %q", firstByte(payloadBytes))
	}

	metaBytes, err := marshalNoEscape(map[string]Meta{"_meta": e.Meta})
	if err != nil {
		return nil, err
	}
	// metaBytes is `{"_meta":{...}}` and payloadBytes is `{...}`. Combine by
	// dropping the closing `}` of meta, dropping the opening `{` of payload,
	// and joining with `,`. If payload is the empty object `{}` we don't add
	// the comma.
	out := make([]byte, 0, len(metaBytes)+len(payloadBytes))
	out = append(out, metaBytes[:len(metaBytes)-1]...) // drop trailing }
	if len(payloadBytes) > 2 {                         // not "{}"
		out = append(out, ',')
		out = append(out, payloadBytes[1:]...) // include trailing }
	} else {
		out = append(out, '}')
	}
	return out, nil
}

// marshalNoEscape is json.Marshal with HTML escaping disabled, trimmed of
// the trailing newline the json.Encoder always appends.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	return b, nil
}

func firstByte(b []byte) string {
	if len(b) == 0 {
		return "<empty>"
	}
	return string(b[:1])
}

// Write writes the JSON envelope for cmd + payload to w. Payload MUST marshal
// to a JSON object (a struct or map, not a slice/string/number); use a wrapper
// struct like `type listResp struct { Projects []Project ´json:"projects"´ }`
// when you want a single field at the top level.
func Write(w io.Writer, cmd string, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Avoid escaping & < > in URLs/HTML — agent consumers parse this raw, not
	// in a browser.
	enc.SetEscapeHTML(false)
	return enc.Encode(envelope{
		Meta:    Meta{Crocs: SchemaVersion, Command: cmd},
		Payload: payload,
	})
}

// MustWrite is the convenience wrapper that fails the process on encode
// errors. Use when there's no caller in a position to recover (i.e. main).
func MustWrite(cmd string, payload any) {
	if err := Write(os.Stdout, cmd, payload); err != nil {
		fmt.Fprintln(os.Stderr, "crocs: output encode failed:", err)
		os.Exit(1)
	}
}
