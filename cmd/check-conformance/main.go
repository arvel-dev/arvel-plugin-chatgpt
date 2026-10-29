// SPDX-License-Identifier: Apache-2.0

// Command check-conformance runs the released SDK checker with isolated fake inputs.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/arvel-dev/arvel-sdk/procx"
	"github.com/arvel-dev/arvel-sdk/testkit/scenario"
	"gopkg.in/yaml.v3"
)

const sdkVersion = "v0.2.0"

var errTeardown = errors.New("conformance process teardown failed")

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type report struct {
	Kind   string  `json:"kind"`
	Level  string  `json:"level"`
	Failed int     `json:"failed"`
	Checks []check `json:"checks"`
}

func main() {
	output := flag.String("report", "", "optional sanitized JSON report path")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(ctx, *output, os.Stdout, logger); err != nil {
		logger.Error("Conformance failed", "error", err)
		os.Exit(1)
	}
}

// execute always joins the owned process tree; raw child diagnostics stay private.
func execute(ctx context.Context, root, path string, args []string, stdout io.Writer) error {
	env := os.Environ()
	if len(args) > 0 && args[0] == "build" {
		var clean []string
		for _, v := range env {
			if !strings.HasPrefix(v, "CGO_ENABLED=") {
				clean = append(clean, v)
			}
		}
		env = append(clean, "CGO_ENABLED=0")
	}
	p, err := procx.Start(ctx, procx.Spec{Path: path, Args: args, Dir: root, Env: env, Stdout: stdout})
	if err != nil {
		return errors.New("start conformance process")
	}
	code, err := p.Wait()
	if err != nil {
		return errTeardown
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if code != 0 {
		return fmt.Errorf("conformance process exit %d", code)
	}
	return nil
}

func run(ctx context.Context, output string, stdout io.Writer, log *slog.Logger) (resultErr error) {
	root, err := os.Getwd()
	if err != nil {
		return errors.New("resolve repository root")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return errors.New("go toolchain unavailable")
	}
	goBin, err = filepath.Abs(goBin)
	if err != nil {
		return err
	}
	var version bytes.Buffer
	if err = execute(ctx, root, goBin, []string{"list", "-m", "-f", "{{.Version}}", "github.com/arvel-dev/arvel-sdk"}, &version); err != nil {
		return err
	}
	if strings.TrimSpace(version.String()) != sdkVersion {
		return errors.New("unexpected SDK pin")
	}
	temp, err := os.MkdirTemp("", "arvel-conformance-")
	if err != nil {
		return err
	}
	// Every execute returns only after its process tree has joined.
	defer cleanupScratch(temp, &resultErr, log, os.RemoveAll)
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(temp, name)
	}
	for _, build := range []struct{ name, pkg string }{{"plugin", "./cmd/arvel-plugin-chatgpt"}, {"fake-codex", "./cmd/fake-codex"}, {"checker", "github.com/arvel-dev/arvel-sdk/cmd/arvel-plugin-check"}} {
		log.Info("Building conformance executable", "component", build.name)
		if err = execute(ctx, root, goBin, []string{"build", "-o", exe(build.name), build.pkg}, nil); err != nil {
			return fmt.Errorf("build %s: %w", build.name, err)
		}
	}
	envPath, scenarios, err := prepare(root, temp, exe("fake-codex"))
	if err != nil {
		return err
	}
	var raw boundedReport
	args := []string{"--json", "--level", "release", "--manifest", filepath.Join(root, "arvel.json"), "--scenarios", scenarios, "--test-env", envPath, "--", exe("plugin")}
	log.Info("Running isolated release conformance", "sdk", sdkVersion)
	runErr := execute(ctx, root, exe("checker"), args, &raw)
	if raw.Len() > 8<<20 {
		return errors.New("checker report exceeds limit")
	}
	if output != "" && json.Valid(raw.Bytes()) {
		if err = os.WriteFile(output, raw.Bytes(), 0600); err != nil {
			return errors.New("write sanitized checker report")
		}
	}
	if runErr != nil {
		return runErr
	}
	if err = validateReport(raw.Bytes()); err != nil {
		return err
	}
	_, err = stdout.Write(raw.Bytes())
	return err
}

func prepare(root, temp, fake string) (string, string, error) {
	data, err := os.ReadFile(filepath.Join(root, "conformance-env.yaml"))
	if err != nil {
		return "", "", err
	}
	var env map[string]any
	if yaml.Unmarshal(data, &env) != nil {
		return "", "", errors.New("invalid environment template")
	}
	init, ok := env["initialize"].(map[string]any)
	if !ok {
		return "", "", errors.New("missing initialize template")
	}
	connection, ok := init["connection"].(map[string]any)
	if !ok {
		return "", "", errors.New("missing connection template")
	}
	connection["config"] = map[string]any{"codexPath": fake}
	encoded, err := yaml.Marshal(env)
	if err != nil {
		return "", "", err
	}
	envPath := filepath.Join(temp, "environment.yaml")
	if err = os.WriteFile(envPath, encoded, 0600); err != nil {
		return "", "", err
	}
	dir := filepath.Join(temp, "conformance")
	if err = os.Mkdir(dir, 0700); err != nil {
		return "", "", err
	}
	files, err := filepath.Glob(filepath.Join(root, "conformance", "*.yaml"))
	if err != nil || len(files) == 0 {
		return "", "", errors.New("no conformance scenarios")
	}
	source := filepath.Join(temp, "skill-source")
	if err = os.Mkdir(source, 0700); err != nil {
		return "", "", err
	}
	if err = os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: owned\ndescription: Synthetic conformance skill.\n---\nOWNED_SKILL_BODY\n"), 0600); err != nil {
		return "", "", err
	}
	for _, file := range files {
		if _, err := scenario.LoadFile(file); err != nil {
			return "", "", errors.New("invalid scenario template")
		}
		original, err := os.ReadFile(file)
		if err != nil {
			return "", "", err
		}
		var sc map[string]any
		if yaml.Unmarshal(original, &sc) != nil {
			return "", "", errors.New("invalid scenario mapping")
		}
		if init, ok := sc["initialize"].(map[string]any); ok {
			if conn, ok := init["connection"].(map[string]any); ok {
				conn["config"] = map[string]any{"codexPath": fake}
			}
		}
		sc = replaceSkill(sc, source).(map[string]any)
		data, err := yaml.Marshal(sc)
		if err != nil {
			return "", "", err
		}
		if err = os.WriteFile(filepath.Join(dir, filepath.Base(file)), data, 0600); err != nil {
			return "", "", err
		}
	}
	return envPath, dir, nil
}

func replaceSkill(v any, path string) any {
	switch value := v.(type) {
	case string:
		if value == "CONFORMANCE_SKILL_SOURCE" {
			return path
		}
	case map[string]any:
		for k, v := range value {
			value[k] = replaceSkill(v, path)
		}
	case []any:
		for i, v := range value {
			value[i] = replaceSkill(v, path)
		}
	}
	return v
}

func validateReport(raw []byte) error {
	var r report
	if json.Unmarshal(raw, &r) != nil || r.Level != "release" || r.Kind != "provider" || r.Failed != 0 {
		return errors.New("invalid or failing release report")
	}
	required := map[string]bool{"initialize": false, "status": false, "models": false, "concurrency": false, "cancel": false, "busy": false, "proxy": false}
	scenarios := 0
	for _, c := range r.Checks {
		if c.Status != "pass" {
			return fmt.Errorf("required check did not pass: %s", c.Name)
		}
		if _, ok := required[c.Name]; ok {
			required[c.Name] = true
		}
		if strings.HasPrefix(c.Name, "scenario ") && !strings.Contains(c.Name, " · step ") {
			scenarios++
		}
	}
	for name, pass := range required {
		if !pass {
			return fmt.Errorf("missing required check: %s", name)
		}
	}
	if scenarios == 0 {
		return errors.New("empty conformance scenario report")
	}
	return nil
}

func cleanupScratch(path string, result *error, log *slog.Logger, remove func(string) error) {
	if errors.Is(*result, errTeardown) {
		log.Error("Unconfirmed process teardown; scratch retained")
		return
	}
	if err := remove(path); err != nil {
		log.Error("Conformance scratch cleanup failed")
		*result = errors.Join(*result, errors.New("conformance scratch cleanup failed"))
	}
}

// boundedReport rejects oversized checker output before allocating it.
type boundedReport struct{ buffer bytes.Buffer }

func (b *boundedReport) Write(p []byte) (int, error) {
	if len(p) > (8<<20)-b.Len() {
		return 0, errors.New("checker report exceeds limit")
	}
	return b.buffer.Write(p)
}

func (b *boundedReport) Len() int      { return b.buffer.Len() }
func (b *boundedReport) Bytes() []byte { return b.buffer.Bytes() }
