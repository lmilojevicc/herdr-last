package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// Client communicates with Herdr over a Unix domain socket using JSON-RPC 2.0.
type Client struct {
	socketPath string
	reqID      uint64
}

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      string      `json:"id"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      string          `json:"id"`
}

// RPCError holds error information returned from an RPC call.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("RPC error %s: %s", e.Code, e.Message)
}

// Event represents a focus event emitted by Herdr.
type Event struct {
	Name        string
	WorkspaceID string
	TabID       string
}

type eventEnvelope struct {
	Event string `json:"event"`
	Data  struct {
		Type        string `json:"type"`
		WorkspaceID string `json:"workspace_id"`
		TabID       string `json:"tab_id"`
	} `json:"data"`
}

// Workspace represents a Herdr workspace item.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ResolveSocketPath resolves socket location from HERDR_SOCKET_PATH, HERDR_SESSION,
// or fallback ~/.config/herdr/herdr.sock.
func ResolveSocketPath() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	if sess := os.Getenv("HERDR_SESSION"); sess != "" {
		if filepath.IsAbs(sess) && (filepath.Ext(sess) == ".sock" || fileExists(sess)) {
			return sess
		}
		candidate := filepath.Join(os.TempDir(), fmt.Sprintf("herdr-%s.sock", sess))
		if fileExists(candidate) {
			return candidate
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// New creates a Client pointing to the resolved socket path.
func New() *Client {
	return &Client{
		socketPath: ResolveSocketPath(),
	}
}

// NewWithPath creates a Client pointing to a custom socket path.
func NewWithPath(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
	}
}

// SocketPath returns the configured Unix socket path.
func (c *Client) SocketPath() string {
	return c.socketPath
}

func (c *Client) nextRequestID() string {
	return strconv.FormatUint(atomic.AddUint64(&c.reqID, 1), 10)
}

// Call executes a single JSON-RPC request over the Unix socket.
func (c *Client) Call(method string, params interface{}, result interface{}) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to socket %s: %w", c.socketPath, err)
	}
	defer conn.Close()

	id := c.nextRequestID()
	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      id,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if resp.ID != id {
		return fmt.Errorf("response ID %q does not match request ID %q", resp.ID, id)
	}
	if resp.Error != nil {
		return resp.Error
	}

	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}

	return nil
}

// FocusWorkspace switches Herdr focus to the given workspace.
func (c *Client) FocusWorkspace(wsID string) error {
	herdrBin := os.Getenv("HERDR_BIN_PATH")
	if herdrBin == "" {
		herdrBin = "herdr"
	}
	cmd := exec.Command(herdrBin, "workspace", "focus", wsID)
	if _, err := cmd.CombinedOutput(); err == nil {
		return nil
	}
	return c.Call("workspace.focus", map[string]string{"workspace_id": wsID}, nil)
}

// FocusTab switches Herdr focus to the given tab in a workspace.
func (c *Client) FocusTab(_ string, tabID string) error {
	herdrBin := os.Getenv("HERDR_BIN_PATH")
	if herdrBin == "" {
		herdrBin = "herdr"
	}
	cmd := exec.Command(herdrBin, "tab", "focus", tabID)
	if _, err := cmd.CombinedOutput(); err == nil {
		return nil
	}
	return c.Call("tab.focus", map[string]string{"tab_id": tabID}, nil)
}

// ListWorkspaces lists all workspaces from Herdr.
func (c *Client) ListWorkspaces() ([]Workspace, error) {
	var workspaces []Workspace
	if err := c.Call("workspace.list", nil, &workspaces); err != nil {
		return nil, err
	}
	return workspaces, nil
}

// DecodeEvent decodes Herdr's documented event envelope.
func DecodeEvent(data []byte) (Event, error) {
	var envelope eventEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Event{}, fmt.Errorf("invalid event JSON: %w", err)
	}

	name := envelope.Event
	if name == "" {
		name = envelope.Data.Type
	}
	switch name {
	case "workspace_focused", "workspace.focused":
		name = "workspace.focused"
	case "tab_focused", "tab.focused":
		name = "tab.focused"
	default:
		return Event{}, fmt.Errorf("unsupported Herdr event %q", name)
	}

	return Event{
		Name:        name,
		WorkspaceID: envelope.Data.WorkspaceID,
		TabID:       envelope.Data.TabID,
	}, nil
}

// Subscribe subscribes to Herdr events and streams events to the handler callback.
func (c *Client) Subscribe(events []string, handler func(evt Event)) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to socket %s: %w", c.socketPath, err)
	}
	defer conn.Close()

	subscriptions := make([]map[string]string, 0, len(events))
	for _, event := range events {
		subscriptions = append(subscriptions, map[string]string{"type": event})
	}

	id := c.nextRequestID()
	req := Request{
		JSONRPC: "2.0",
		Method:  "events.subscribe",
		Params:  map[string]interface{}{"subscriptions": subscriptions},
		ID:      id,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal subscribe request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to send subscribe request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read subscribe response: %w", err)
		}
		return fmt.Errorf("subscription closed before Herdr responded")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("failed to decode subscribe response: %w", err)
	}
	if resp.ID != id {
		return fmt.Errorf("response ID %q does not match request ID %q", resp.ID, id)
	}
	if resp.Error != nil {
		return resp.Error
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		evt, err := DecodeEvent(line)
		if err != nil {
			return fmt.Errorf("failed to decode subscribed event: %w", err)
		}
		handler(evt)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed while reading subscribed events: %w", err)
	}
	return nil
}
