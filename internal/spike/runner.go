// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arvel-dev/arvel-sdk/jsonl"
	"github.com/arvel-dev/arvel-sdk/procx"
	"github.com/arvel-dev/arvel-sdk/redact"
)

// RunOptions configures a fixed development probe. No arbitrary RPC is exposed.
type RunOptions struct {
	Mode, Codex, Scenario, Home, Out, Schema, Index, In, Notices string
	Timeout                                                      time.Duration
	// DeviceCode displays a challenge only to the person completing this private-home probe.
	DeviceCode func(verificationURL, userCode string) error
}

// Entry is one scrubbed message in an ordered, direction-aware transcript.
type Entry struct {
	Direction string          `json:"direction"`
	Message   json.RawMessage `json:"message"`
}

// Run executes a recording command. Real CLI work requires ARVEL_E2E=1.
func Run(ctx context.Context, o RunOptions) error {
	if o.Timeout <= 0 {
		o.Timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	switch o.Mode {
	case "surface":
		b, err := LoadBundle(ctx, o.Schema)
		if err != nil {
			return err
		}
		if o.Out == "" {
			return errors.New("surface requires --out")
		}
		return b.Surface(o.Out)
	case "verify":
		return Verify(ctx, o.Schema, o.Index)
	case "sanitize":
		return sanitizeFile(ctx, o.In, o.Out)
	case "schema", "record", "sandbox":
		if os.Getenv("ARVEL_E2E") != "1" {
			return errors.New("real probes require ARVEL_E2E=1")
		}
	default:
		return errors.New("unknown recording command")
	}
	if o.Mode == "sandbox" {
		return errors.New("sandbox probe requires an actual Arvel P9 launcher and native target; not available")
	}
	if o.Out == "" {
		return errors.New("probe requires --out")
	}
	if o.Mode == "record" {
		if o.Home == "" {
			o.Home = "private"
		}
		if o.Home != "private" && o.Home != "shared" {
			return errors.New("home must be private or shared")
		}
		if !knownScenario(o.Scenario) {
			return errors.New("unknown fixed scenario")
		}
		if (o.Scenario == "device-cancel" || o.Scenario == "device-login" || strings.HasPrefix(o.Scenario, "env-") || o.Scenario == "private-api-key") && o.Home != "private" {
			return errors.New("login probes require a private home")
		}
		if _, err := os.Stat(o.Out); err == nil {
			return errors.New("recording destination already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if override := os.Getenv("ARVEL_CODEX_BIN"); o.Codex == "" && override != "" && os.Getenv("ARVEL_DEV") == "1" {
		o.Codex = override
	}
	if o.Codex == "" {
		var err error
		o.Codex, err = procx.FindBinary("codex")
		if err != nil {
			return errors.New("codex binary was not found")
		}
	}
	if !filepath.IsAbs(o.Codex) {
		return errors.New("--codex must be absolute")
	}
	scratch, err := os.MkdirTemp("", "arvel-spike-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	home := filepath.Join(scratch, "codex-home")
	if err := os.Mkdir(home, 0700); err != nil {
		return err
	}
	privateHome := home
	if o.Mode != "schema" && o.Home == "shared" {
		privateHome = ""
	}
	env := probeEnvironment(privateHome, "")
	if o.Scenario == "env-openai" {
		env = append(env, "OPENAI_API_KEY=synthetic-not-a-real-key", "OPENAI_BASE_URL=http://127.0.0.1:1")
	}
	if o.Scenario == "env-codex" {
		env = append(env, "CODEX_API_KEY=synthetic-not-a-real-key")
	}
	version, err := probeVersion(ctx, o.Codex, scratch, env)
	if err != nil {
		return err
	}
	if o.Mode == "schema" {
		return generateSchema(ctx, o, scratch, env, version)
	}
	b, err := LoadBundle(ctx, o.Schema)
	if err != nil {
		return err
	}
	var entries []Entry
	err = recordWithDeviceCode(ctx, procx.Spec{Path: o.Codex, Args: []string{"app-server"}, Dir: scratch, Env: env}, b, o.Scenario, &entries, o.DeviceCode)
	if err == nil {
		err = requiredObservation(entries, o.Scenario)
	}
	if len(entries) > 0 {
		if saveErr := saveRecording(o.Out, entries, map[string]any{"version": version, "schemaVersion": filepath.Base(o.Schema), "os": runtime.GOOS, "arch": runtime.GOARCH, "scenario": o.Scenario, "home": o.Home, "recordedAt": time.Now().UTC().Format(time.RFC3339), "sanitization": "in-memory before persistence", "completed": err == nil}); saveErr != nil {
			return saveErr
		}
	}
	return err
}

func probeEnvironment(home, proxy string) []string {
	b := procx.Env().Strip("OPENAI_", "CODEX_", "RUST_LOG").Set("RUST_LOG", "warn").Proxy(proxy)
	if home != "" {
		b.Set("CODEX_HOME", home)
	}
	return b.Build()
}

func requiredObservation(entries []Entry, scenario string) error {
	wantItem := ""
	wantMethod := ""
	wantMarker := ""
	switch scenario {
	case "command-approval":
		wantMethod = "item/commandExecution/requestApproval"
	case "command":
		wantItem = "commandExecution"
	case "web":
		wantItem = "webSearch"
	case "mcp":
		wantItem = "mcpToolCall"
	case "subagent", "subagent-v1", "subagent-enabled", "subagent-persistent", "subagent-capacity", "subagent-complete":
		wantItem = "collabAgentToolCall"
	case "reasoning":
		wantMethod = "item/reasoning/summaryTextDelta"
	case "plan", "plan-enabled":
		wantMethod = "turn/plan/updated"
	case "image":
		wantMarker = "IMAGE_READY"
	case "instructions":
		wantMarker = "ARVEL_INSTRUCTIONS_PONG"
	case "skills-explicit", "skills-selected":
		wantMarker = "SKILL_PONG"
	case "skills":
		wantMarker = "arvel-probe"
	default:
		return nil
	}
	for _, entry := range entries {
		if entry.Direction != "server_to_client" {
			continue
		}
		var m wireMessage
		if json.Unmarshal(entry.Message, &m) != nil {
			continue
		}
		if wantMarker != "" {
			if scenario == "skills" && len(m.Result) > 0 && strings.Contains(string(m.Result), wantMarker) {
				return nil
			}
			var params struct {
				Item struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
			}
			if m.Method == "item/completed" && json.Unmarshal(m.Params, &params) == nil && params.Item.Type == "agentMessage" && strings.Contains(params.Item.Text, wantMarker) {
				return nil
			}
		}
		if wantMethod != "" && m.Method == wantMethod {
			return nil
		}
		if wantItem != "" && m.Method == "item/completed" {
			var params struct {
				Item struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"item"`
			}
			if json.Unmarshal(m.Params, &params) == nil && params.Item.Type == wantItem && (params.Item.Status == "completed" || params.Item.Status == "") {
				return nil
			}
		}
	}
	return errors.New("required feature observation was not emitted; completed turn alone is insufficient")
}

var scenarioNames = []string{"handshake", "handshake-jsonrpc", "preinitialize", "account", "models", "limits", "thread", "text", "reasoning", "interrupt", "edit-read-only", "edit-workspace", "edit-accept", "edit-session", "edit-cancel", "resume", "image", "plan", "plan-enabled", "command", "command-approval", "web", "mcp", "subagent", "subagent-v1", "subagent-enabled", "subagent-persistent", "subagent-capacity", "subagent-complete", "instructions", "skills", "skills-explicit", "skills-selected", "failed", "env-stripped", "env-openai", "env-codex", "private-api-key", "device-cancel", "device-login", "network-allow", "network-deny"}

// Scenarios returns the fixed development probe names supported by this tool.
func Scenarios() []string { return append([]string(nil), scenarioNames...) }

func knownScenario(s string) bool {
	for _, name := range scenarioNames {
		if s == name {
			return true
		}
	}
	return false
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4096 {
		return 0, errors.New("bounded command output exceeded")
	}
	return b.Buffer.Write(p)
}

func probeVersion(ctx context.Context, binary, dir string, env []string) (string, error) {
	var out limitedBuffer
	p, err := procx.Start(ctx, procx.Spec{Path: binary, Args: []string{"--version"}, Dir: dir, Env: env, Stdout: &out})
	if err != nil {
		return "", errors.New("start version probe")
	}
	code, err := p.Wait()
	if err != nil || code != 0 {
		return "", errors.New("version probe failed")
	}
	fields := strings.Fields(out.String())
	if len(fields) != 2 || fields[0] != "codex-cli" {
		return "", errors.New("unrecognized Codex version output")
	}
	for _, r := range fields[1] {
		if !(r >= '0' && r <= '9' || r == '.' || r == '-' || r >= 'a' && r <= 'z') {
			return "", errors.New("unsafe version string")
		}
	}
	return fields[1], nil
}

func generateSchema(ctx context.Context, o RunOptions, scratch string, env []string, version string) error {
	stage := filepath.Join(scratch, "schema")
	p, err := procx.Start(ctx, procx.Spec{Path: o.Codex, Args: []string{"app-server", "generate-json-schema", "--out", stage}, Dir: scratch, Env: env})
	if err != nil {
		return errors.New("start schema generator")
	}
	code, err := p.Wait()
	if err != nil || code != 0 {
		return errors.New("schema generation failed")
	}
	if _, err := LoadBundle(ctx, stage); err != nil {
		return err
	}
	if _, err := os.Stat(o.Out); err == nil {
		return compareSchema(stage, o.Out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if o.Notices == "" {
		return errors.New("new schema version requires --notices with matching upstream LICENSE and NOTICE")
	}
	for _, name := range []string{"LICENSE", "NOTICE"} {
		data, err := os.ReadFile(filepath.Join(o.Notices, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, name), data, 0600); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(stage, "provenance.json"), map[string]string{"version": version, "os": runtime.GOOS, "arch": runtime.GOARCH, "command": "codex app-server generate-json-schema --out <directory>", "experimental": "not enabled"}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.Out), 0700); err != nil {
		return err
	}
	// Copy rather than rename: the destination may be on another volume.
	return os.CopyFS(o.Out, os.DirFS(stage))
}

func compareSchema(stage, existing string) error {
	seen := map[string]bool{}
	err := filepath.WalkDir(stage, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		seen[rel] = true
		a, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(existing, rel))
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return fmt.Errorf("schema changed: %s", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(existing, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" || entry.Name() == "surface.json" || entry.Name() == "provenance.json" {
			return nil
		}
		rel, err := filepath.Rel(existing, path)
		if err != nil {
			return err
		}
		if !seen[rel] {
			return fmt.Errorf("schema removed: %s", rel)
		}
		return nil
	})
}

type wireMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type peer struct {
	redactor        *redact.Redactor
	input           *io.PipeWriter
	read            <-chan json.RawMessage
	readErr         <-chan error
	bundle          *Bundle
	entries         *[]Entry
	next            int
	serverIDs       map[string]bool
	notifications   []wireMessage
	jsonRPC         bool
	approvedFiles   map[string]bool
	cwd             string
	decision        string
	allowFixtureMCP bool
	networkSeen     *atomic.Int32
	networkAllowed  bool
	deviceCode      func(string, string) error
}

func record(ctx context.Context, spec procx.Spec, b *Bundle, scenario string, entries *[]Entry) error {
	return recordWithDeviceCode(ctx, spec, b, scenario, entries, nil)
}

func recordWithDeviceCode(ctx context.Context, spec procx.Spec, b *Bundle, scenario string, entries *[]Entry, deviceCode func(string, string) error) error {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	spec.Stdin = inR
	spec.Stdout = outW
	spec.Stderr = io.Discard
	childCtx, cancel := context.WithCancel(ctx)
	p, err := procx.Start(childCtx, spec)
	if err != nil {
		cancel()
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return errors.New("start app-server")
	}
	read := make(chan json.RawMessage)
	readErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(read)
		scanner := jsonl.NewScanner(outR, 0)
		for scanner.Scan() {
			if err := scanner.Err(); err != nil {
				readErr <- errors.New("invalid app-server line length")
				return
			}
			raw := append(json.RawMessage(nil), scanner.Bytes()...)
			select {
			case read <- raw:
			case <-childCtx.Done():
				return
			}
		}
		readErr <- scanner.Err()
	}()
	wg.Add(1)
	go func() { defer wg.Done(); <-p.Done(); _ = outW.Close() }()
	stop := context.AfterFunc(childCtx, func() { _ = inW.Close(); _ = outR.Close() })
	defer func() {
		cancel()
		stop()
		_ = inW.Close()
		_ = inR.Close()
		p.KillTree(100 * time.Millisecond)
		_, _ = p.Wait()
		_ = outW.Close()
		_ = outR.Close()
		wg.Wait()
	}()
	peer := &peer{input: inW, read: read, readErr: readErr, bundle: b, entries: entries, serverIDs: map[string]bool{}, approvedFiles: map[string]bool{}, cwd: spec.Dir, decision: "decline", deviceCode: deviceCode}
	return peer.scenario(ctx, scenario, spec.Dir)
}

func (p *peer) capture(direction string, raw json.RawMessage) error {
	if p.redactor == nil {
		p.redactor = redact.New()
	}
	clean, err := sanitizeWithRedactor(raw, p.redactor)
	if err != nil {
		return err
	}
	*p.entries = append(*p.entries, Entry{direction, clean})
	return nil
}

func (p *peer) send(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := p.capture("client_to_server", raw); err != nil {
		return err
	}
	_, err = p.input.Write(append(raw, '\n'))
	return err
}

func (p *peer) receive(ctx context.Context) (wireMessage, error) {
	for {
		select {
		case <-ctx.Done():
			return wireMessage{}, ctx.Err()
		case raw, ok := <-p.read:
			if !ok {
				select {
				case err := <-p.readErr:
					if err != nil {
						return wireMessage{}, err
					}
				default:
				}
				return wireMessage{}, io.EOF
			}
			var m wireMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				return wireMessage{}, errors.New("app-server sent invalid JSON")
			}
			if err := p.capture("server_to_client", raw); err != nil {
				return wireMessage{}, err
			}
			if m.Method == "item/started" {
				var params struct {
					Item struct {
						ID      string `json:"id"`
						Type    string `json:"type"`
						Changes []struct {
							Path string `json:"path"`
						} `json:"changes"`
					} `json:"item"`
				}
				if json.Unmarshal(m.Params, &params) == nil && params.Item.Type == "fileChange" && len(params.Item.Changes) > 0 {
					allowed := true
					for _, change := range params.Item.Changes {
						if !allowedProbeFile(p.cwd, change.Path) {
							allowed = false
						}
					}
					p.approvedFiles[params.Item.ID] = allowed
				}
			}
			if m.Method != "" && len(m.ID) > 0 {
				key := string(m.ID)
				if p.serverIDs[key] {
					return wireMessage{}, errors.New("duplicate server request ID")
				}
				p.serverIDs[key] = true
				var response any
				switch m.Method {
				case "item/commandExecution/requestApproval":
					response = map[string]any{"id": m.ID, "result": map[string]any{"decision": "decline"}}
				case "item/fileChange/requestApproval":
					var params struct {
						ItemID string `json:"itemId"`
					}
					_ = json.Unmarshal(m.Params, &params)
					decision := "decline"
					if p.approvedFiles[params.ItemID] {
						decision = p.decision
					}
					response = map[string]any{"id": m.ID, "result": map[string]any{"decision": decision}}
				case "item/permissions/requestApproval":
					response = map[string]any{"id": m.ID, "result": map[string]any{"permissions": map[string]any{}, "scope": "turn"}}
				case "mcpServer/elicitation/request":
					var params struct {
						Server string `json:"serverName"`
						Mode   string `json:"mode"`
						Schema struct {
							Type       string         `json:"type"`
							Properties map[string]any `json:"properties"`
						} `json:"requestedSchema"`
					}
					_ = json.Unmarshal(m.Params, &params)
					action := "decline"
					if p.allowFixtureMCP && params.Server == "arvel_probe" && params.Mode == "form" && params.Schema.Type == "object" && len(params.Schema.Properties) == 0 {
						action = "accept"
					}
					response = map[string]any{"id": m.ID, "result": map[string]any{"action": action, "content": map[string]any{}}}
				default:
					response = map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "Unsupported by recording probe"}}
				}
				if err := p.send(response); err != nil {
					return wireMessage{}, err
				}
				continue
			}
			return m, nil
		}
	}
}

func allowedProbeFile(cwd, path string) bool {
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return false
	}
	return filepath.Clean(parent) == filepath.Clean(root) && filepath.Base(path) == "arvel_probe.txt"
}

func (p *peer) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.next++
	id := p.next
	value := map[string]any{"id": id, "method": method, "params": params}
	if p.jsonRPC {
		value["jsonrpc"] = "2.0"
	}
	if err := p.bundle.validate("ClientRequest.json", value); err != nil {
		return nil, err
	}
	if err := p.send(value); err != nil {
		return nil, err
	}
	for {
		m, err := p.receive(ctx)
		if err != nil {
			return nil, err
		}
		if m.Method != "" {
			p.notifications = append(p.notifications, m)
			continue
		}
		if string(m.ID) != fmt.Sprint(id) {
			return nil, errors.New("unexpected response ID")
		}
		if len(m.Error) > 0 {
			return m.Error, errors.New("app-server returned an RPC error; see scrubbed transcript")
		}
		return m.Result, nil
	}
}

func (p *peer) scenario(ctx context.Context, scenario, cwd string) error {
	p.allowFixtureMCP = scenario == "mcp"
	if scenario == "edit-accept" {
		p.decision = "accept"
	}
	if scenario == "edit-session" {
		p.decision = "acceptForSession"
	}
	if scenario == "edit-cancel" {
		p.decision = "cancel"
	}
	if scenario == "handshake-jsonrpc" {
		p.jsonRPC = true
	}
	if scenario == "preinitialize" {
		if _, err := p.request(ctx, "account/read", map[string]any{"refreshToken": false}); err == nil {
			return errors.New("uninitialized app-server accepted account request")
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if _, err := p.request(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "arvel", "title": "Arvel", "version": "0.1.0"}}); err != nil {
		return err
	}
	if err := p.send(map[string]any{"method": "initialized"}); err != nil {
		return err
	}
	selectedSkillPath := filepath.Join(cwd, ".agents", "skills", "arvel-probe", "SKILL.md")
	if scenario == "skills" || scenario == "skills-explicit" || scenario == "skills-selected" {
		dir := filepath.Join(cwd, ".agents", "skills", "arvel-probe")
		if err := os.MkdirAll(filepath.Join(dir, "agents"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: arvel-probe\ndescription: Use only when explicitly asked for the harmless probe marker.\n---\nReply SKILL_PONG without using tools.\n"), 0600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "agents", "openai.yaml"), []byte("policy:\n  allow_implicit_invocation: false\n"), 0600); err != nil {
			return err
		}
		result, err := p.request(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true})
		if err != nil || scenario == "skills" {
			return err
		}
		if scenario == "skills-selected" {
			var listing struct {
				Data []struct {
					Skills []struct{ Name, Path string } `json:"skills"`
				} `json:"data"`
			}
			if json.Unmarshal(result, &listing) != nil {
				return errors.New("invalid skills list")
			}
			expected, err := filepath.EvalSymlinks(selectedSkillPath)
			if err != nil {
				return err
			}
			found := false
			for _, row := range listing.Data {
				for _, skill := range row.Skills {
					if skill.Name == "arvel-probe" {
						actual, err := filepath.EvalSymlinks(skill.Path)
						if err == nil && actual == expected {
							selectedSkillPath = skill.Path
							found = true
						}
					}
				}
			}
			if !found {
				return errors.New("scratch skill absent from discovered catalog")
			}
		}
	}
	if strings.HasPrefix(scenario, "env-") || scenario == "private-api-key" {
		if scenario == "private-api-key" {
			if _, err := p.request(ctx, "account/login/start", map[string]any{"type": "apiKey", "apiKey": "synthetic-not-a-real-key"}); err != nil {
				return err
			}
		}
		result, err := p.request(ctx, "account/read", map[string]any{"refreshToken": false})
		if err != nil {
			return err
		}
		var state struct {
			Account *struct {
				Type string `json:"type"`
			} `json:"account"`
		}
		if json.Unmarshal(result, &state) != nil {
			return errors.New("invalid account observation")
		}
		if scenario != "private-api-key" && state.Account != nil {
			return errors.New("environment unexpectedly selected an account")
		}
		if scenario == "private-api-key" {
			if state.Account == nil || state.Account.Type != "apiKey" {
				return errors.New("private key login did not select the API-key account type")
			}
			if _, err := p.request(ctx, "account/logout", nil); err != nil {
				return err
			}
			result, err = p.request(ctx, "account/read", map[string]any{"refreshToken": false})
			if err != nil {
				return err
			}
			state.Account = nil
			if json.Unmarshal(result, &state) != nil || state.Account != nil {
				return errors.New("private logout retained an account")
			}
		}
		return nil
	}
	if scenario == "device-cancel" || scenario == "device-login" {
		if scenario == "device-login" && p.deviceCode == nil {
			return errors.New("device login requires an interactive challenge display")
		}
		result, err := p.request(ctx, "account/login/start", map[string]any{"type": "chatgptDeviceCode"})
		if err != nil {
			return err
		}
		var login struct {
			ID   string `json:"loginId"`
			URL  string `json:"verificationUrl"`
			Code string `json:"userCode"`
		}
		if json.Unmarshal(result, &login) != nil || login.ID == "" {
			return errors.New("device-code response has no login ID")
		}
		if scenario == "device-login" {
			if login.URL != "https://auth.openai.com/codex/device" || login.Code == "" || len(login.Code) > 32 || strings.ContainsAny(login.Code, "\r\n\x1b") {
				return errors.New("unexpected device challenge")
			}
			if err := p.deviceCode(login.URL, login.Code); err != nil {
				return err
			}
			for {
				var m wireMessage
				if len(p.notifications) > 0 {
					m = p.notifications[0]
					p.notifications = p.notifications[1:]
				} else {
					m, err = p.receive(ctx)
					if err != nil {
						return err
					}
				}
				if m.Method != "account/login/completed" {
					continue
				}
				var completed struct {
					ID      string `json:"loginId"`
					Success bool   `json:"success"`
				}
				if json.Unmarshal(m.Params, &completed) != nil || completed.ID != login.ID {
					continue
				}
				if !completed.Success {
					return errors.New("device login did not succeed")
				}
				result, err = p.request(ctx, "account/read", map[string]any{"refreshToken": false})
				if err != nil {
					return err
				}
				var account struct {
					Account *struct {
						Type string `json:"type"`
					} `json:"account"`
				}
				if json.Unmarshal(result, &account) != nil || account.Account == nil || account.Account.Type != "chatgpt" {
					return errors.New("completed device login has no ChatGPT account")
				}
				return nil
			}
		}
		if _, err := p.request(ctx, "account/login/cancel", map[string]any{"loginId": login.ID}); err != nil {
			return err
		}
		result, err = p.request(ctx, "account/read", map[string]any{"refreshToken": false})
		if err != nil {
			return err
		}
		var account struct {
			Account any `json:"account"`
		}
		if json.Unmarshal(result, &account) != nil || account.Account != nil {
			return errors.New("cancelled device login unexpectedly signed in")
		}
		return nil
	}
	switch scenario {
	case "handshake", "handshake-jsonrpc", "preinitialize":
		return nil
	case "account":
		_, err := p.request(ctx, "account/read", map[string]any{"refreshToken": false})
		return err
	case "models":
		_, err := p.request(ctx, "model/list", map[string]any{})
		return err
	case "limits":
		_, err := p.request(ctx, "account/rateLimits/read", map[string]any{})
		return err
	}
	sandbox := "read-only"
	var probeServer *httptest.Server
	if scenario == "network-allow" || scenario == "network-deny" {
		p.networkSeen = &atomic.Int32{}
		p.networkAllowed = scenario == "network-allow"
		probeServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.networkSeen.Add(1)
			_, _ = io.WriteString(w, "NETWORK_PONG")
		}))
		defer probeServer.Close()
		sandbox = "workspace-write"
	}
	if scenario == "edit-workspace" {
		sandbox = "workspace-write"
	}
	threadParams := map[string]any{"cwd": cwd, "approvalPolicy": "on-request", "sandbox": sandbox, "ephemeral": scenario != "resume"}
	// Probe-only model observed with multiAgentVersion v1; never a production default.
	if scenario == "subagent-v1" || scenario == "reasoning" {
		threadParams["model"] = "gpt-5.6-luna"
	}
	if scenario == "mcp" {
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		threadParams["config"] = map[string]any{"mcp_servers": map[string]any{"arvel_probe": map[string]any{"command": binary, "args": []any{"mcp-fixture"}}}, "sandbox_workspace_write": map[string]any{"network_access": false}}
	}
	if probeServer != nil {
		threadParams["config"] = map[string]any{"sandbox_workspace_write": map[string]any{"network_access": p.networkAllowed}}
	}
	if scenario == "plan-enabled" {
		threadParams["config"] = map[string]any{"tools": map[string]any{"update_plan": map[string]any{"enabled": true}}}
	}
	if scenario == "subagent-enabled" {
		threadParams["model"] = "gpt-5.6-luna"
		threadParams["config"] = map[string]any{"agents": map[string]any{"enabled": true, "max_concurrent_threads_per_session": 1, "max_depth": 1, "default_subagent_model": "gpt-5.6-luna"}}
	}
	if scenario == "subagent-persistent" || scenario == "subagent-capacity" || scenario == "subagent-complete" {
		threadParams["ephemeral"] = false
		threadParams["config"] = map[string]any{"agents": map[string]any{"enabled": true, "max_depth": 1}, "features": map[string]any{"multi_agent_v2": map[string]any{"enabled": true, "disable_native_tools": false, "max_concurrent_threads_per_session": 1, "wait_agent_enabled": true}}}
	}
	if scenario == "subagent-capacity" || scenario == "subagent-complete" {
		threadParams["config"].(map[string]any)["features"].(map[string]any)["multi_agent_v2"].(map[string]any)["max_concurrent_threads_per_session"] = 2
	}

	if scenario == "instructions" {
		threadParams["developerInstructions"] = "When the user asks for the marker, reply with exactly ARVEL_INSTRUCTIONS_PONG. Do not use tools."
	}
	res, err := p.request(ctx, "thread/start", threadParams)
	if err != nil {
		return err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &started); err != nil || started.Thread.ID == "" {
		return errors.New("thread response has no ID")
	}
	if scenario == "thread" {
		return nil
	}
	prompt := "Reply with exactly PONG. Do not use any tools."
	switch scenario {
	case "skills-explicit", "skills-selected":
		prompt = "Use the explicitly selected arvel-probe skill and follow its response instruction. Do not use any other tools."
	case "reasoning":
		prompt = "Without tools, find the smallest positive integer n such that n is divisible by 7 and its decimal representation contains only digits 0 and 1. Provide just the integer and a concise verification, not private reasoning."
	case "plan", "plan-enabled":
		prompt = "Use the update_plan tool to record two pending steps named inspect and report. Then reply READY. Do not use any other tool or execute the steps."
	case "command-approval":
		prompt = "For this harmless approval probe, request elevated sandbox permissions for exactly the pwd command, even though pwd normally needs none. This exercises a client-declined approval. If declined, do not retry any command or use any other tool; reply DECLINED."
	case "command":
		prompt = "Run exactly the pwd shell command in the current directory and then reply READY. Do not run any other command or use network tools."
	case "web":
		prompt = "Use the built-in web_search tool once to find the official OpenAI Codex CLI documentation. Then reply READY. Do not use shell or MCP tools."
	case "mcp":
		prompt = "Call only arvel_probe's probe_echo MCP tool once, then repeat the marker it returns. Do not call any other tool."
	case "subagent", "subagent-v1", "subagent-enabled", "subagent-persistent", "subagent-capacity", "subagent-complete":
		prompt = "Spawn exactly one subagent with the task: reply CHILD_PONG without any tools or child agents. Wait for its reply and finish with READY. Do not execute commands or use network/MCP tools."
	case "instructions":
		prompt = "Please provide the marker."
	case "image":
		prompt = "An attached image is a generated one-pixel test image. Reply IMAGE_READY. Do not use any tools."
	}
	if strings.HasPrefix(scenario, "edit-") {
		prompt = "Use apply_patch to create arvel_probe.txt in the current directory containing exactly PROBE. Do not run shell commands, use network tools, or modify any other file. If the sandbox refuses the edit, report that refusal."
	}
	if probeServer != nil {
		prompt = "Run exactly curl --silent --show-error --max-time 5 '" + probeServer.URL + "/probe' once. Do not run other commands or request permission if blocked. Then report the result."
	}
	input := []any{map[string]any{"type": "text", "text": prompt}}
	if scenario == "image" {
		path := filepath.Join(cwd, "probe.png")
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
		pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
		encodeErr := png.Encode(file, pixel)
		closeErr := file.Close()
		if encodeErr != nil {
			return encodeErr
		}
		if closeErr != nil {
			return closeErr
		}
		input = append(input, map[string]any{"type": "localImage", "path": path})
	}
	if scenario == "skills-explicit" || scenario == "skills-selected" {
		input = append(input, map[string]any{"type": "skill", "name": "arvel-probe", "path": selectedSkillPath})
	}
	turnParams := map[string]any{"threadId": started.Thread.ID, "input": input}
	if scenario == "reasoning" {
		turnParams["effort"] = "high"
		turnParams["summary"] = "detailed"
	}
	if scenario == "failed" {
		turnParams["model"] = "arvel-deliberately-missing-model"
	}
	res, err = p.request(ctx, "turn/start", turnParams)
	if err != nil {
		return err
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(res, &turn); err != nil || turn.Turn.ID == "" {
		return errors.New("turn response has no ID")
	}
	if scenario == "interrupt" {
		if _, err := p.request(ctx, "turn/interrupt", map[string]any{"threadId": started.Thread.ID, "turnId": turn.Turn.ID}); err != nil {
			return err
		}
	}
	for {
		for _, notification := range p.notifications {
			if targetTurnCompleted(notification, started.Thread.ID, turn.Turn.ID) {
				return p.finishTurn(ctx, notification.Params, scenario, cwd, started.Thread.ID)
			}
		}
		p.notifications = nil
		m, err := p.receive(ctx)
		if err != nil {
			return err
		}
		if targetTurnCompleted(m, started.Thread.ID, turn.Turn.ID) {
			return p.finishTurn(ctx, m.Params, scenario, cwd, started.Thread.ID)
		}
	}
}

func targetTurnCompleted(m wireMessage, threadID, turnID string) bool {
	if m.Method != "turn/completed" {
		return false
	}
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	return json.Unmarshal(m.Params, &params) == nil && params.ThreadID == threadID && params.Turn.ID == turnID
}

func (p *peer) finishTurn(ctx context.Context, raw json.RawMessage, scenario, cwd, threadID string) error {
	if err := verifyTurn(raw, scenario, cwd); err != nil {
		return err
	}
	if p.networkSeen != nil {
		if reached := p.networkSeen.Load() > 0; reached != p.networkAllowed {
			return errors.New("loopback request did not match the configured native network policy")
		}
		found := false
		for _, entry := range *p.entries {
			if strings.Contains(string(entry.Message), `"type":"commandExecution"`) {
				found = true
			}
		}
		if !found {
			return errors.New("no command execution was observed for the network probe")
		}
	}
	if scenario == "resume" {
		_, err := p.request(ctx, "thread/resume", map[string]any{"threadId": threadID})
		return err
	}
	return nil
}

func verifyTurn(raw json.RawMessage, scenario, cwd string) error {
	var payload struct {
		Turn struct {
			Status string `json:"status"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return errors.New("invalid turn completion")
	}
	expected := "completed"
	if scenario == "failed" {
		expected = "failed"
	}
	if scenario == "interrupt" || scenario == "edit-cancel" {
		expected = "interrupted"
	}
	if payload.Turn.Status != expected {
		return errors.New("turn did not reach the scenario's required terminal status")
	}
	if strings.HasPrefix(scenario, "edit-") {
		data, err := os.ReadFile(filepath.Join(cwd, "arvel_probe.txt"))
		shouldExist := scenario == "edit-workspace" || scenario == "edit-accept" || scenario == "edit-session"
		if shouldExist && (err != nil || strings.TrimSpace(string(data)) != "PROBE") {
			return errors.New("approved scratch file was not created correctly")
		}
		if !shouldExist && !errors.Is(err, os.ErrNotExist) {
			return errors.New("declined scratch edit unexpectedly wrote a file")
		}
	}
	return nil
}

func saveRecording(dir string, entries []Entry, metadata any) error {
	if _, err := os.Stat(dir); err == nil {
		return errors.New("recording destination already exists")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var out bytes.Buffer
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		out.Write(data)
		out.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), out.Bytes(), 0600); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "metadata.json"), metadata)
}

func sanitizeFile(ctx context.Context, input, output string) error {
	if input == "" || output == "" {
		return errors.New("sanitize requires --in and --out")
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	s := jsonl.NewScanner(f, 0)
	var out bytes.Buffer
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Err(); err != nil {
			return err
		}
		clean, err := Sanitize(s.Bytes())
		if err != nil {
			return err
		}
		out.Write(clean)
		out.WriteByte('\n')
	}
	if err := s.Err(); err != nil {
		return err
	}
	return os.WriteFile(output, out.Bytes(), 0600)
}
