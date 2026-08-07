package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveSocketPath(t *testing.T) {
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/custom-herdr.sock")
	if path := ResolveSocketPath(); path != "/tmp/custom-herdr.sock" {
		t.Fatalf("expected override, got %s", path)
	}

	t.Setenv("HERDR_SOCKET_PATH", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".config", "herdr", "herdr.sock")
	if path := ResolveSocketPath(); path != expected {
		t.Fatalf("expected %s, got %s", expected, path)
	}
}

func listenOnce(t *testing.T, handle func(net.Conn) error) string {
	t.Helper()
	socketDir, err := os.MkdirTemp("/tmp", "herdr-last-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	sockPath := filepath.Join(socketDir, "test.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	errs := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			err = handle(conn)
		}
		errs <- err
	}()
	t.Cleanup(func() {
		listener.Close()
		if err := <-errs; err != nil && !strings.Contains(err.Error(), "closed network connection") {
			t.Errorf("mock server: %v", err)
		}
	})
	return sockPath
}

func TestFocusWorkspaceRPCUsesHerdrSchema(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "missing-herdr"))
	sockPath := listenOnce(t, func(conn net.Conn) error {
		var raw map[string]interface{}
		if err := json.NewDecoder(conn).Decode(&raw); err != nil {
			return err
		}
		id, ok := raw["id"].(string)
		if !ok {
			return fmt.Errorf("request ID is not a string: %#v", raw["id"])
		}
		if raw["method"] != "workspace.focus" {
			return fmt.Errorf("unexpected method: %v", raw["method"])
		}
		params, _ := raw["params"].(map[string]interface{})
		if !reflect.DeepEqual(params, map[string]interface{}{"workspace_id": "ws-123"}) {
			return fmt.Errorf("unexpected params: %#v", params)
		}
		return json.NewEncoder(conn).Encode(Response{JSONRPC: "2.0", ID: id, Result: json.RawMessage(`{"status":"ok"}`)})
	})

	if err := NewWithPath(sockPath).FocusWorkspace("ws-123"); err != nil {
		t.Fatalf("FocusWorkspace failed: %v", err)
	}
}

func TestFocusTabRPCUsesHerdrSchema(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "missing-herdr"))
	sockPath := listenOnce(t, func(conn net.Conn) error {
		var raw map[string]interface{}
		if err := json.NewDecoder(conn).Decode(&raw); err != nil {
			return err
		}
		id, ok := raw["id"].(string)
		if !ok {
			return fmt.Errorf("request ID is not a string: %#v", raw["id"])
		}
		if raw["method"] != "tab.focus" {
			return fmt.Errorf("unexpected method: %v", raw["method"])
		}
		params, _ := raw["params"].(map[string]interface{})
		if !reflect.DeepEqual(params, map[string]interface{}{"tab_id": "tab-123"}) {
			return fmt.Errorf("unexpected params: %#v", params)
		}
		return json.NewEncoder(conn).Encode(Response{JSONRPC: "2.0", ID: id, Result: json.RawMessage(`{"status":"ok"}`)})
	})

	if err := NewWithPath(sockPath).FocusTab("ws-ignored", "tab-123"); err != nil {
		t.Fatalf("FocusTab failed: %v", err)
	}
}

func TestCallDecodesStringErrorCode(t *testing.T) {
	sockPath := listenOnce(t, func(conn net.Conn) error {
		var req Request
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return err
		}
		return json.NewEncoder(conn).Encode(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: "invalid_params", Message: "bad target"},
		})
	})

	err := NewWithPath(sockPath).Call("workspace.focus", map[string]string{"workspace_id": "bad"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid_params") {
		t.Fatalf("expected string-coded RPC error, got %v", err)
	}
}

func TestFocusUsesInjectedHerdrBinary(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "args.log")
	binPath := filepath.Join(tmp, "herdr")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HERDR_TEST_LOG\"\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", binPath)
	t.Setenv("HERDR_TEST_LOG", logPath)

	c := NewWithPath(filepath.Join(tmp, "missing.sock"))
	if err := c.FocusWorkspace("ws-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.FocusTab("ws-1", "tab-2"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "workspace focus ws-1\ntab focus tab-2\n"
	if string(got) != want {
		t.Fatalf("injected binary args = %q, want %q", got, want)
	}
}

func TestSubscribeUsesSchemaAndDecodesEventEnvelope(t *testing.T) {
	sockPath := listenOnce(t, func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(line, &raw); err != nil {
			return err
		}
		id, ok := raw["id"].(string)
		if !ok {
			return fmt.Errorf("request ID is not a string: %#v", raw["id"])
		}
		wantParams := map[string]interface{}{"subscriptions": []interface{}{
			map[string]interface{}{"type": "workspace.focused"},
			map[string]interface{}{"type": "tab.focused"},
		}}
		if !reflect.DeepEqual(raw["params"], wantParams) {
			return fmt.Errorf("unexpected subscription params: %#v", raw["params"])
		}
		enc := json.NewEncoder(conn)
		if err := enc.Encode(Response{JSONRPC: "2.0", ID: id, Result: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		if err := enc.Encode(map[string]interface{}{
			"event": "workspace_focused",
			"data":  map[string]string{"type": "workspace_focused", "workspace_id": "ws-1"},
		}); err != nil {
			return err
		}
		return enc.Encode(map[string]interface{}{
			"event": "tab_focused",
			"data":  map[string]string{"type": "tab_focused", "workspace_id": "ws-1", "tab_id": "tab-2"},
		})
	})

	var got []Event
	err := NewWithPath(sockPath).Subscribe(
		[]string{"workspace.focused", "tab.focused"},
		func(evt Event) { got = append(got, evt) },
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Name: "workspace.focused", WorkspaceID: "ws-1"},
		{Name: "tab.focused", WorkspaceID: "ws-1", TabID: "tab-2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}
