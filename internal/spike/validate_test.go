// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify_ValidatesRealRecordings(t *testing.T) {
	if err := Verify(context.Background(), "", filepath.Join("..", "..", "testdata", "fixtures", "index.json")); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_RejectsBrokenCorrelationAndPayloads(t *testing.T) {
	b := testBundle(t)
	valid, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "handshake", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(valid)), "\n")
	for _, tc := range []struct {
		name string
		data string
	}{
		{"unanswered", lines[0] + "\n"},
		{"duplicate response", lines[0] + "\n" + lines[1] + "\n" + lines[1] + "\n"},
		{"malformed envelope", "{}\n"},
		{"fractional request ID", strings.ReplaceAll(string(valid), `"id":1`, `"id":1.0000000000000001`)},
		{"wrong type", strings.Replace(string(valid), `"id":1`, `"id":false`, 1)},
		{"secret", strings.Replace(string(valid), `"name":"arvel"`, `"name":"person@company.example"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(p, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := verifyTranscript(context.Background(), b, p); err == nil {
				t.Fatal("invalid transcript passed")
			}
		})
	}
}

func TestVerify_RejectsUnindexedRecording(t *testing.T) {
	dir := t.TempDir()
	schema, err := filepath.Abs(filepath.Join("..", "..", "testdata", "schema", "0.164.0"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(FixtureIndex{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), index, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), schema, filepath.Join(dir, "index.json")); err == nil {
		t.Fatal("unindexed recording passed")
	}
}
