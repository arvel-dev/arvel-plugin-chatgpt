// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/procx"
)

func TestEnvironment_StripsAmbientCredentialsAndPreservesProxy(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK"} {
		t.Setenv(name, "synthetic-parent-sentinel")
	}
	home := t.TempDir()
	env := probeEnvironment(home, "http://127.0.0.1:12345")
	values := map[string]string{}
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		values[strings.ToUpper(k)] = v
	}
	if values["CODEX_HOME"] != home || values["RUST_LOG"] != "warn" {
		t.Fatal("private environment was not applied")
	}
	for _, key := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		if values[key] != "http://127.0.0.1:12345" {
			t.Errorf("proxy missing: %s", key)
		}
	}
	if values["NO_PROXY"] != "localhost,127.0.0.1" {
		t.Fatal("proxy loopback exceptions missing")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	env = append(env, "SPIKE_TEST_MODE=environment")
	var out bytes.Buffer
	p, err := procx.Start(ctx, procx.Spec{Path: binary, Args: []string{"-test.run=^TestRecorderHelper$"}, Env: env, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	code, err := p.Wait()
	if err != nil || code != 0 {
		t.Fatal("environment helper failed")
	}
	var present map[string]bool
	if err := json.Unmarshal(out.Bytes(), &present); err != nil {
		t.Fatal(err)
	}
	for key, found := range present {
		if found {
			t.Errorf("credential variable inherited: %s", key)
		}
	}
	for _, entry := range probeEnvironment("", "") {
		if strings.HasPrefix(entry, "CODEX_HOME=") {
			t.Fatal("shared mode changed CODEX_HOME")
		}
	}
}
