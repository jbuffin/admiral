package harnessmodels_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the harness CLI: a dock whose env holds ADMIRAL_FAKE_LISTER runs fakeHarness.
// The shapes below are what the real claude 2.1.329, codex-cli 0.169.0 and copilot 1.0.114 printed,
// noise lines included. The dock's env picks the behavior:
//
//	FAKE_MODELS      comma-separated model names the harness offers
//	FAKE_MODE        ok (default), fail (stderr boom, exit 3), hang (never answers) or garbage
//	FAKE_RECORD_DIR  gets argv (one line) and pid
func TestMain(m *testing.M) {
	if os.Getenv("ADMIRAL_FAKE_LISTER") != "" {
		fakeHarness()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeHarness() {
	if dir := os.Getenv("FAKE_RECORD_DIR"); dir != "" {
		_ = os.WriteFile(dir+"/argv", []byte(strings.Join(os.Args[1:], " ")), 0o600)
		_ = os.WriteFile(dir+"/pid", []byte(fmt.Sprint(os.Getpid())), 0o600)
	}
	switch os.Getenv("FAKE_MODE") {
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(3)
	case "hang":
		time.Sleep(time.Minute)
	case "garbage":
		fmt.Println("this is not json")
		fmt.Println(`{"unexpected":true}`)
		time.Sleep(time.Minute)
	}
	models := strings.Split(os.Getenv("FAKE_MODELS"), ",")
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	readRequest := func() map[string]any {
		line, err := in.ReadString('\n')
		if err != nil {
			os.Exit(4)
		}
		var req map[string]any
		if json.Unmarshal([]byte(line), &req) != nil {
			fmt.Fprintln(os.Stderr, "unreadable request:", line)
			os.Exit(5)
		}
		return req
	}
	emit := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	// The ids are whatever the lister sent, as the real harnesses answer them.
	argv := strings.Join(os.Args[1:], " ")
	switch {
	case strings.Contains(argv, "stream-json"):
		req := readRequest()
		inner, _ := req["request"].(map[string]any)
		if req["type"] != "control_request" || inner["subtype"] != "initialize" {
			os.Exit(6)
		}
		var out []map[string]any
		out = append(out, map[string]any{"value": "default", "displayName": "Default (recommended)"})
		for _, m := range models {
			out = append(out, map[string]any{"value": m, "displayName": m})
		}
		emit(map[string]any{"type": "system", "subtype": "hook_started"})
		emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": req["request_id"],
			// the real line was about 109KB, past a default bufio.Scanner's 64KB
			"response": map[string]any{"commands": strings.Repeat("x", 100000), "models": out, "pid": os.Getpid()}}})
		emit(map[string]any{"type": "system", "subtype": "commands_changed"})
	case strings.Contains(argv, "app-server"):
		init := readRequest()
		if init["method"] != "initialize" {
			os.Exit(6)
		}
		emit(map[string]any{"id": init["id"], "result": map[string]any{"codexHome": "/x", "platformOs": "macos"}})
		emit(map[string]any{"method": "account/updated", "params": map[string]any{"authMode": "chatgpt", "planType": "team"}})
		note := readRequest()
		if note["method"] != "initialized" || note["id"] != nil {
			os.Exit(6)
		}
		entry := func(m string, hidden bool) map[string]any {
			return map[string]any{"id": m, "model": m, "displayName": m, "hidden": hidden}
		}
		for page := 0; ; page++ {
			req := readRequest()
			if req["method"] != "model/list" {
				os.Exit(6)
			}
			params, _ := req["params"].(map[string]any)
			if page == 0 {
				if params["cursor"] != nil {
					os.Exit(7)
				}
				emit(map[string]any{"id": req["id"], "result": map[string]any{"data": []any{entry(models[0], false), entry("gpt-hidden", true)}, "nextCursor": "c2"}})
				continue
			}
			if params["cursor"] != "c2" {
				os.Exit(7)
			}
			var rest []any
			for _, m := range models[1:] {
				rest = append(rest, entry(m, false))
			}
			emit(map[string]any{"id": req["id"], "result": map[string]any{"data": rest, "nextCursor": nil}})
			break
		}
	case strings.Contains(argv, "--acp"):
		init := readRequest()
		if init["method"] != "initialize" {
			os.Exit(6)
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": init["id"], "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "Copilot"}}})
		req := readRequest()
		params, _ := req["params"].(map[string]any)
		servers, isList := params["mcpServers"].([]any)
		if req["method"] != "session/new" || params["cwd"] == "" || params["cwd"] == nil || !isList || len(servers) != 0 {
			os.Exit(6)
		}
		emit(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s"}})
		// the real copilot lists auto twice
		avail := []map[string]any{{"modelId": "auto", "name": "Auto"}, {"modelId": "auto", "name": "Auto"}}
		for _, m := range models {
			avail = append(avail, map[string]any{"modelId": m, "name": m})
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"sessionId": "s", "models": map[string]any{"availableModels": avail, "currentModelId": "auto"}}})
	default:
		os.Exit(8)
	}
	// Like the real CLIs, it stays up after answering until it is killed.
	time.Sleep(time.Minute)
}
