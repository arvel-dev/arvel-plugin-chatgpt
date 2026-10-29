// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/testkit/scenario"
)

func TestValidateReport_RequiresActualAssertions(t *testing.T) {
	good := report{Kind: "provider", Level: "release", Checks: []check{}}
	for _, name := range []string{"initialize", "status", "models", "concurrency", "cancel", "busy", "proxy", "scenario proxy.yaml"} {
		good.Checks = append(good.Checks, check{Name: name, Status: "pass"})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*report)
	}{
		{"pass", func(*report) {}},
		{"no active cancel", func(r *report) { r.Checks[4].Status = "skip" }},
		{"no proxy", func(r *report) { r.Checks = r.Checks[:6] }},
		{"failed fixture", func(r *report) { r.Failed = 1; r.Checks[0].Status = "fail" }},
		{"empty scenarios", func(r *report) { r.Checks = r.Checks[:7] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := good
			r.Checks = append([]check(nil), good.Checks...)
			tc.mutate(&r)
			raw, _ := json.Marshal(r)
			if (validateReport(raw) == nil) != (tc.name == "pass") {
				t.Fatal("incorrect release acceptance")
			}
		})
	}
	if validateReport([]byte(`{}`)) == nil || validateReport(nil) == nil {
		t.Fatal("invalid report accepted")
	}
}

func TestPrepare_PreservesOmittedKindAndTypedInputs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	fake := filepath.Join(temp, "fake with spaces")
	env, dir, err := prepare(root, temp, fake)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(env); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("missing scenarios")
	}
	for _, file := range files {
		sc, err := scenario.LoadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(file) == "private-login.yaml" {
			if sc.Initialize.Connection.Kind != "chatgpt" || sc.Initialize.Connection.Config["codexPath"] != fake {
				t.Fatal("kind override lost fake path")
			}
		}
		if filepath.Base(file) == "stream-resume.yaml" && sc.Initialize.KindSpecified() {
			t.Fatal("omitted kind became explicit empty kind")
		}
	}
	if filepath.Dir(env) == dir {
		t.Fatal("environment is not an executable scenario")
	}
}

func TestExecute_CancellationJoinsProcess(t *testing.T) {
	t.Setenv("ARVEL_CONFORMANCE_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err = execute(ctx, t.TempDir(), binary, []string{"-test.run=^TestConformanceHelper$"}, nil); err == nil {
		t.Fatal("cancelled helper passed")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("process cancellation did not finish")
	}
}
func TestConformanceHelper(t *testing.T) {
	if os.Getenv("ARVEL_CONFORMANCE_HELPER") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestCleanup_RequiresConfirmedTeardownAndPropagatesFailure(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	path := t.TempDir()
	result := errTeardown
	cleanupScratch(path, &result, log, func(string) error { t.Error("unconfirmed resources must not be deleted"); return nil })
	if _, err := os.Stat(path); err != nil {
		t.Fatal("retained scratch lost")
	}
	primary := errors.New("primary failure")
	result = primary
	cleanupScratch(path, &result, log, func(string) error { return errors.New("synthetic removal failure") })
	if !errors.Is(result, primary) || result == primary {
		t.Fatal("cleanup failure masked or primary failure lost")
	}
	result = nil
	cleanupScratch(path, &result, log, os.RemoveAll)
	if result != nil {
		t.Fatal(result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("confirmed scratch not removed")
	}
}

func TestConfiguredChecker_RejectsBrokenFixtureInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production/checker/fake executables")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goBin, err = filepath.Abs(goBin)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, b := range []struct{ name, pkg string }{{"plugin", "./cmd/arvel-plugin-chatgpt"}, {"fake", "./cmd/fake-codex"}, {"checker", "github.com/arvel-dev/arvel-sdk/cmd/arvel-plugin-check"}} {
		if err = execute(ctx, root, goBin, []string{"build", "-o", exe(b.name), b.pkg}, nil); err != nil {
			t.Fatal(err)
		}
	}
	envFile, scenarios, err := prepare(root, dir, exe("fake"))
	if err != nil {
		t.Fatal(err)
	}
	// One ordinary scenario plus all builtin probes suffices for these negative runs.
	files, err := filepath.Glob(filepath.Join(scenarios, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		if filepath.Base(p) != "proxy.yaml" {
			if err = os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	original, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	proxyFile := filepath.Join(scenarios, "proxy.yaml")
	proxy, err := os.ReadFile(proxyFile)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, level string
		mutate      func(map[string]any)
	}{
		{"missing kind", "basic", func(env map[string]any) {
			delete(env["initialize"].(map[string]any)["connection"].(map[string]any), "kind")
		}},
		{"undeclared kind", "basic", func(env map[string]any) {
			env["initialize"].(map[string]any)["connection"].(map[string]any)["kind"] = "undeclared"
		}},
		{"missing secret", "basic", func(env map[string]any) {
			delete(env["initialize"].(map[string]any)["connection"].(map[string]any), "secrets")
		}},
		{"cleared inherited secret", "basic", func(env map[string]any) {
			env["checks"].(map[string]any)["basic"] = map[string]any{"initialize": map[string]any{"connection": map[string]any{"secrets": map[string]any{}}}}
		}},
		{"finished before cancel", "release", func(env map[string]any) {
			env["checks"].(map[string]any)["cancel"].(map[string]any)["prompt"] = []any{map[string]any{"type": "text", "text": "plain"}}
		}},
		{"no streaming activity", "release", func(env map[string]any) {
			env["checks"].(map[string]any)["cancel"].(map[string]any)["prompt"] = []any{map[string]any{"type": "text", "text": "wait"}}
		}},
		{"fake process crash", "release", func(env map[string]any) {
			env["checks"].(map[string]any)["cancel"].(map[string]any)["prompt"] = []any{map[string]any{"type": "text", "text": "crash"}}
		}},
		{"missing proxy evidence", "release", func(map[string]any) {
			if err := os.WriteFile(proxyFile, bytes.ReplaceAll(proxy, []byte("conformance:proxy"), []byte("plain")), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(proxyFile, proxy, 0600); err != nil {
				t.Fatal(err)
			}
			var env map[string]any
			if yaml.Unmarshal(original, &env) != nil {
				t.Fatal("decode environment")
			}
			tc.mutate(env)
			raw, err := yaml.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(envFile, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var report bytes.Buffer
			err = execute(ctx, root, exe("checker"), []string{"--json", "--level", tc.level, "--manifest", filepath.Join(root, "arvel.json"), "--scenarios", scenarios, "--test-env", envFile, "--", exe("plugin")}, &report)
			if err == nil {
				t.Fatal("invalid fixture passed checker")
			}
			if bytes.Contains(report.Bytes(), []byte("sk-conformance-synthetic")) {
				t.Fatal("synthetic credential leaked into failed report")
			}
			var decoded struct {
				Failed int `json:"failed"`
			}
			if json.Unmarshal(report.Bytes(), &decoded) != nil || decoded.Failed == 0 {
				t.Fatal("expected explicit failed check report")
			}
		})
	}
}

func TestCheckerReport_IsBoundedWhileReceiving(t *testing.T) {
	var b boundedReport
	if _, err := b.Write(make([]byte, 8<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("overflow")); err == nil || b.Len() != 8<<20 {
		t.Fatal("checker output exceeded memory budget")
	}
}

func TestCheckerReport_CopyCannotBypassBudget(t *testing.T) {
	var b boundedReport
	source := io.LimitReader(bytes.NewReader(make([]byte, (8<<20)+1)), (8<<20)+1)
	if _, err := io.Copy(&b, source); err == nil || b.Len() > 8<<20 {
		t.Fatal("io.Copy bypassed checker output budget")
	}
}
