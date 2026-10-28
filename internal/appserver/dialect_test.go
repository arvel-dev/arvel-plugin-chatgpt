// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/jsonl"
)

func reader(raw string) *liteReader {
	return &liteReader{scanner: jsonl.NewScanner(strings.NewReader(raw), 0), state: &dialect{active: map[string]bool{}}}
}
func TestDialect_PreservesEnvelopeAndIntegerIDs(t *testing.T) {
	for _, line := range []string{`{"id":9007199254740993,"result":{"text":"<safe>"}}`, `{"jsonrpc":"2.0","id":"server","method":"approve","params":{}}`} {
		r := reader(line + "\n")
		raw, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["jsonrpc"]) != `"2.0"` {
			t.Fatal("missing SDK envelope")
		}
		var out bytes.Buffer
		w := liteWriter{output: &out, state: r.state}
		if _, err := w.Write(raw); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out.Bytes(), []byte(`"jsonrpc"`)) {
			t.Fatal("strict envelope leaked")
		}
		if strings.Contains(line, "9007199254740993") && !strings.Contains(out.String(), "9007199254740993") {
			t.Fatal("integer ID lost precision")
		}
	}
}
func TestDialect_RejectsInvalidAndActiveDuplicateRequests(t *testing.T) {
	for _, raw := range []string{"[]\n", "not json\n", `{"id":1.5,"method":"x"}` + "\n", `{"id":"a","method":"x"}` + "\n" + `{"id":"\u0061","method":"x"}` + "\n", `{"jsonrpc":"1.0","method":"x"}` + "\n", `{"id":"same","method":"x"}` + "\n" + `{"id":"same","method":"x"}` + "\n"} {
		if _, err := io.ReadAll(reader(raw)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestDialect_EnforcesOriginal64MiBBoundary(t *testing.T) {
	const prefix = `{"method":"note","params":{"text":"`
	const suffix = `"}}`
	size := jsonl.DefaultMaxLine - len(prefix) - len(suffix)
	r := reader(prefix + strings.Repeat("<", size) + suffix + "\n")
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatalf("exact boundary refused: %v", err)
	}
	r = reader(prefix + strings.Repeat("x", size+1) + suffix + "\n")
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, jsonl.ErrLineTooLong) {
		t.Fatal("oversized frame accepted")
	}
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }
func TestDialect_PropagatesShortWrites(t *testing.T) {
	w := liteWriter{output: shortWriter{}, state: &dialect{active: map[string]bool{}}}
	if _, err := w.Write([]byte(`{"jsonrpc":"2.0","method":"initialized"}`)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
}
