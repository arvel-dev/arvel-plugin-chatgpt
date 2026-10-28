// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/arvel-dev/arvel-sdk/jsonl"
)

// ServeMCP is a fixed development fixture server, never a production service.
// Its initialize/tools/list envelope follows the SDK checker's MCP test stub.
func ServeMCP(ctx context.Context, input io.Reader, output io.Writer) error {
	if closer, ok := input.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stop()
	}
	s := jsonl.NewScanner(input, 0)
	encoder := json.NewEncoder(output)
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Err(); err != nil {
			return err
		}
		var req wireMessage
		if err := json.Unmarshal(s.Bytes(), &req); err != nil {
			return errors.New("malformed MCP fixture request")
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "arvel-probe", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "probe_echo", "description": "Return the fixed harmless test marker MCP_PONG", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.Name != "probe_echo" {
				return errors.New("unknown MCP fixture tool")
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "MCP_PONG"}}}
		case "ping":
			result = map[string]any{}
		default:
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}}); err != nil {
				return err
			}
			continue
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	return s.Err()
}
