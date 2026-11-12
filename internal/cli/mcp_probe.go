package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"

	"github.com/jbuffin/admiral/internal/probes"
)

// MCPProbe is the tiny MCP server the fence probe starts: one tool, and a file written for the
// probe only when that tool is called, so the probe sees the call without trusting the model.
func MCPProbe(args []string, std IO) error {
	if len(args) > 0 {
		return usagef("%s", Usage("mcp-probe"))
	}
	dir, nonce := os.Getenv(probes.DirEnv), os.Getenv(probes.NonceEnv)
	if !probes.ValidEnv(dir, nonce) {
		return usagef("admiral mcp-probe is the fence probe's server: %s and %s are not set", probes.DirEnv, probes.NonceEnv)
	}
	in := bufio.NewReader(std.In)
	send := func(r rpcResponse) {
		r.JSONRPC = "2.0"
		if b, err := json.Marshal(r); err == nil {
			std.Out.Write(append(b, '\n'))
		}
	}
	for {
		line, err := in.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			serveProbeRPC(line, send, func() {
				if f, err := os.OpenFile(probes.CalledPath(dir, nonce), os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
					f.Close()
				}
			})
		}
		if err != nil {
			return nil
		}
	}
}

func serveProbeRPC(line []byte, send func(rpcResponse), called func()) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil || req.Method == "" || req.ID == nil {
		return
	}
	reply := func(result any) { send(rpcResponse{ID: req.ID, Result: result}) }
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpLatest
		if mcpRevisions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		reply(map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]string{"name": probes.FenceServer, "version": "1"}})
	case "tools/list":
		reply(map[string]any{"tools": []map[string]any{{"name": probes.FenceTool, "description": "Replies pong; the fence probe calls it.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}})
	case "tools/call":
		called()
		reply(map[string]any{"content": []map[string]string{{"type": "text", "text": "pong"}}})
	default:
		reply(map[string]any{})
	}
}
