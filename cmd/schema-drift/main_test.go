// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_ExitCodes(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "schema", "0.164.0")
	var out, diagnostics bytes.Buffer
	if code := run(context.Background(), []string{"--baseline", root, "--candidate", root, "--surface", filepath.Join(root, "production-surface.json")}, &out, &diagnostics); code != 0 {
		t.Fatalf("baseline self compare code=%d", code)
	}
	if out.String() != "[]\n" {
		t.Fatal("unexpected self-compare report")
	}
	if code := run(context.Background(), []string{"--candidate", t.TempDir()}, &out, &diagnostics); code != 2 {
		t.Fatal("invalid schema must exit 2")
	}
	if code := run(context.Background(), nil, &out, &diagnostics); code != 2 {
		t.Fatal("missing candidate must exit 2")
	}
	a, b, dir := t.TempDir(), t.TempDir(), t.TempDir()
	for _, item := range []struct{ path, body string }{{filepath.Join(a, "ClientRequest.json"), `{"oneOf":[{"properties":{"method":{"enum":["test"]},"optional":{"type":"string"}}}]}`}, {filepath.Join(b, "ClientRequest.json"), `{"oneOf":[{"properties":{"method":{"enum":["test"]}}}]}`}, {filepath.Join(dir, "surface.json"), `[{"envelope":"ClientRequest.json","method":"test"}]`}} {
		if os.WriteFile(item.path, []byte(item.body), 0600) != nil {
			t.Fatal("write fixture")
		}
	}
	out.Reset()
	if code := run(context.Background(), []string{"--baseline", a, "--candidate", b, "--surface", filepath.Join(dir, "surface.json")}, &out, &diagnostics); code != 1 {
		t.Fatal("removed property must exit 1")
	}
	if !bytes.Contains(out.Bytes(), []byte("property removed")) {
		t.Fatal("missing drift report")
	}
}
