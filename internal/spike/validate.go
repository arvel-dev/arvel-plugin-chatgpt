// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arvel-dev/arvel-sdk/jsonl"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// FixtureIndex pins the schema and explicitly lists every recorded transcript.
type FixtureIndex struct {
	Schema          string            `json:"schema"`
	Recordings      []string          `json:"recordings"`
	SchemaOverrides map[string]string `json:"schemaOverrides,omitempty"`
}

// Verify checks every indexed transcript, its schema and request correlation.
// Paths in the index are relative to the index's directory; schema can also be
// supplied explicitly. Unindexed transcripts are rejected.
func Verify(ctx context.Context, schemaDir, indexPath string) error {
	if indexPath == "" {
		indexPath = filepath.Join("testdata", "fixtures", "index.json")
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	var index FixtureIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return errors.New("invalid fixture index")
	}
	base := filepath.Dir(indexPath)
	if schemaDir == "" {
		schemaDir = filepath.Join(base, filepath.FromSlash(index.Schema))
	}
	b, err := LoadBundle(ctx, schemaDir)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	bundles := map[string]*Bundle{schemaDir: b}
	for _, rel := range index.Recordings {
		if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
			return errors.New("fixture path escapes index directory")
		}
		path := filepath.Clean(filepath.Join(base, filepath.FromSlash(rel)))
		if seen[path] {
			return errors.New("duplicate fixture index entry")
		}
		seen[path] = true
		selected := b
		if override := index.SchemaOverrides[rel]; override != "" {
			dir := filepath.Join(base, filepath.FromSlash(override))
			var ok bool
			selected, ok = bundles[dir]
			if !ok {
				var err error
				selected, err = LoadBundle(ctx, dir)
				if err != nil {
					return err
				}
				bundles[dir] = selected
			}
		}
		if err := verifyTranscript(ctx, selected, path); err != nil {
			return fmt.Errorf("fixture %s: %w", rel, err)
		}
	}
	return filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != base && entry.Name() == "raw" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "transcript.jsonl" && !seen[filepath.Clean(path)] {
			return errors.New("unindexed recorded transcript")
		}
		return nil
	})
}

func verifyTranscript(ctx context.Context, b *Bundle, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := jsonl.NewScanner(f, 0)
	type pendingRequest struct {
		method    methodSchema
		direction string
	}
	pending := map[string]pendingRequest{}
	used := map[string]bool{}
	line := 0
	for s.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Err(); err != nil {
			return err
		}
		var entry Entry
		if err := json.Unmarshal(s.Bytes(), &entry); err != nil {
			return fmt.Errorf("line %d: invalid transcript envelope", line)
		}
		if entry.Direction != "client_to_server" && entry.Direction != "server_to_client" {
			return fmt.Errorf("line %d: invalid direction", line)
		}
		clean, err := Sanitize(entry.Message)
		if err != nil {
			return err
		}
		original, err := jsonschema.UnmarshalJSON(bytes.NewReader(entry.Message))
		if err != nil {
			return errors.New("invalid fixture JSON")
		}
		scrubbed, err := jsonschema.UnmarshalJSON(bytes.NewReader(clean))
		if err != nil {
			return errors.New("invalid sanitized JSON")
		}
		// Equality by canonical JSON catches sanitizer changes without relying on formatting.
		a, _ := json.Marshal(original)
		z, _ := json.Marshal(scrubbed)
		if string(a) != string(z) {
			return fmt.Errorf("line %d: message is not sanitized", line)
		}
		var m wireMessage
		if err := json.Unmarshal(entry.Message, &m); err != nil {
			return err
		}
		if entry.Direction == "client_to_server" && forbiddenOutgoing(m) {
			return fmt.Errorf("line %d: forbidden outgoing policy payload", line)
		}
		request := m.Method != "" && len(m.ID) > 0
		if m.Method != "" {
			envelope := "ClientNotification.json"
			if entry.Direction == "server_to_client" {
				envelope = "ServerNotification.json"
			}
			if request {
				envelope = strings.Replace(envelope, "Notification", "Request", 1)
			}
			method, ok := b.methods[envelope+":"+m.Method]
			if !ok {
				return fmt.Errorf("line %d: method has no recorded schema mapping", line)
			}
			if err := b.validate(envelope, original); err != nil {
				return fmt.Errorf("line %d: %w", line, err)
			}
			if entry.Direction == "client_to_server" && forbiddenOutgoing(m) {
				return fmt.Errorf("line %d: forbidden experimental or policy payload", line)
			}
			if request {
				key := entry.Direction + ":" + string(m.ID)
				if used[key] {
					return fmt.Errorf("line %d: reused request ID", line)
				}
				used[key] = true
				pending[key] = pendingRequest{method, entry.Direction}
			}
			continue
		}
		if len(m.ID) == 0 {
			return fmt.Errorf("line %d: response has no ID", line)
		}
		opposite := "client_to_server"
		if entry.Direction == "client_to_server" {
			opposite = "server_to_client"
		}
		key := opposite + ":" + string(m.ID)
		req, ok := pending[key]
		if !ok {
			return fmt.Errorf("line %d: orphan or duplicate response", line)
		}
		delete(pending, key)
		if (len(m.Result) == 0) == (len(m.Error) == 0) {
			return fmt.Errorf("line %d: response must have exactly one result or error", line)
		}
		if len(m.Error) > 0 {
			var payload struct {
				Code    *int    `json:"code"`
				Message *string `json:"message"`
			}
			if err := json.Unmarshal(m.Error, &payload); err != nil || payload.Code == nil || payload.Message == nil {
				return fmt.Errorf("line %d: invalid generic RPC error", line)
			}
		} else {
			payload, err := jsonschema.UnmarshalJSON(bytes.NewReader(m.Result))
			if err != nil {
				return errors.New("invalid response JSON")
			}
			if err := b.validate(req.method.Response, payload); err != nil {
				return fmt.Errorf("line %d: %w", line, err)
			}
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if line == 0 {
		return errors.New("empty transcript")
	}
	if len(pending) != 0 {
		return errors.New("transcript has unanswered requests")
	}
	return nil
}

func forbiddenOutgoing(m wireMessage) bool {
	var params any
	_ = json.Unmarshal(m.Params, &params)
	var visit func(any) bool
	visit = func(v any) bool {
		switch o := v.(type) {
		case map[string]any:
			for key, val := range o {
				if key == "experimentalApi" || key == "baseInstructions" || key == "acceptWithExecpolicyAmendment" || key == "applyNetworkPolicyAmendment" {
					return true
				}
				if key == "type" && val == "chatgptAuthTokens" {
					return true
				}
				if key == "approvalPolicy" && val == "never" {
					return true
				}
				if key == "sandbox" && val == "danger-full-access" {
					return true
				}
				if key == "decision" {
					if s, ok := val.(string); ok && (s == "acceptWithExecpolicyAmendment" || s == "applyNetworkPolicyAmendment") {
						return true
					}
				}
				if visit(val) {
					return true
				}
			}
		case []any:
			for _, val := range o {
				if visit(val) {
					return true
				}
			}
		}
		return false
	}
	var result any
	_ = json.Unmarshal(m.Result, &result)
	return visit(params) || visit(result)
}
