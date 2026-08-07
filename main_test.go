package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-last/pkg/state"
)

func TestResolveEventUsesContractualEnvironment(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_EVENT", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_TAB_ID", "")
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", `{"event":"tab_focused","data":{"type":"tab_focused","workspace_id":"ws-1","tab_id":"tab-2"}}`)

	name, wsID, tabID := resolveEvent(nil)
	if name != "tab.focused" || wsID != "ws-1" || tabID != "tab-2" {
		t.Fatalf("resolved (%q, %q, %q)", name, wsID, tabID)
	}
}

func TestRunEventUsesHerdrPluginEvent(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("HERDR_PLUGIN_EVENT", "workspace.focused")
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", "")
	t.Setenv("HERDR_WORKSPACE_ID", "ws-contract")
	t.Setenv("HERDR_TAB_ID", "")

	if err := runEvent(nil); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.GetCurrentWorkspace() != "ws-contract" {
		t.Fatalf("workspace = %q, want ws-contract", st.GetCurrentWorkspace())
	}
}

func TestToggleWorkspaceRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	setSuccessfulHerdrBinary(t)

	st := state.New()
	st.OnWorkspaceFocused("ws-a")
	st.OnWorkspaceFocused("ws-b")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HERDR_WORKSPACE_ID", "ws-b")
	t.Setenv("HERDR_TAB_ID", "")
	if err := runToggleWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetCurrentWorkspace() != "ws-a" || loaded.GetPreviousWorkspace() != "ws-b" {
		t.Fatalf("first toggle state = %#v", loaded)
	}

	t.Setenv("HERDR_WORKSPACE_ID", "ws-a")
	if err := runToggleWorkspace(); err != nil {
		t.Fatal(err)
	}
	loaded, err = state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetCurrentWorkspace() != "ws-b" || loaded.GetPreviousWorkspace() != "ws-a" {
		t.Fatalf("second toggle state = %#v", loaded)
	}
}

func TestToggleTabRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	setSuccessfulHerdrBinary(t)

	st := state.New()
	st.OnTabFocused("ws-1", "tab-a")
	st.OnTabFocused("ws-1", "tab-b")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HERDR_WORKSPACE_ID", "ws-1")
	t.Setenv("HERDR_TAB_ID", "tab-b")
	if err := runToggleTab(); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CurrentTabPerWS["ws-1"] != "tab-a" || loaded.GetPreviousTab("ws-1") != "tab-b" {
		t.Fatalf("first toggle state = %#v", loaded)
	}

	t.Setenv("HERDR_TAB_ID", "tab-a")
	if err := runToggleTab(); err != nil {
		t.Fatal(err)
	}
	loaded, err = state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CurrentTabPerWS["ws-1"] != "tab-b" || loaded.GetPreviousTab("ws-1") != "tab-a" {
		t.Fatalf("second toggle state = %#v", loaded)
	}
}

func setSuccessfulHerdrBinary(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", path)
}

func TestToggleActionsAllowEventDuringFocus(t *testing.T) {
	testToggleActionEventInteraction(t, false)
}

func TestToggleActionsSurfacePostFocusPersistenceFailure(t *testing.T) {
	testToggleActionEventInteraction(t, true)
}

func testToggleActionEventInteraction(t *testing.T, breakStateAfterEvent bool) {
	t.Helper()
	tests := []struct {
		name      string
		eventName string
		workspace string
		tab       string
		prepare   func(*testing.T)
		toggle    func() error
	}{
		{
			name:      "workspace",
			eventName: "workspace.focused",
			workspace: "ws-a",
			prepare: func(t *testing.T) {
				st := state.New()
				st.OnWorkspaceFocused("ws-a")
				st.OnWorkspaceFocused("ws-b")
				if err := st.Save(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HERDR_WORKSPACE_ID", "ws-b")
				t.Setenv("HERDR_TAB_ID", "")
			},
			toggle: runToggleWorkspace,
		},
		{
			name:      "tab",
			eventName: "tab.focused",
			workspace: "ws-1",
			tab:       "tab-a",
			prepare: func(t *testing.T) {
				st := state.New()
				st.OnTabFocused("ws-1", "tab-a")
				st.OnTabFocused("ws-1", "tab-b")
				if err := st.Save(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HERDR_WORKSPACE_ID", "ws-1")
				t.Setenv("HERDR_TAB_ID", "tab-b")
			},
			toggle: runToggleTab,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
			tt.prepare(t)
			marker := setEventingHerdrBinary(t, tt.eventName, tt.workspace, tt.tab, breakStateAfterEvent)

			err := tt.toggle()
			if _, markerErr := os.Stat(marker); markerErr != nil {
				t.Fatalf("focus helper did not complete its event transaction: %v", markerErr)
			}
			if breakStateAfterEvent {
				if err == nil {
					t.Fatal("expected persistence failure introduced after successful focus")
				}
				if !strings.Contains(err.Error(), "failed to create state directory") {
					t.Fatalf("expected post-focus state persistence error, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("event hook could not acquire the state lock during focus: %v", err)
			}
		})
	}
}

func setEventingHerdrBinary(t *testing.T, eventName, workspace, tab string, breakStateAfterEvent bool) string {
	t.Helper()
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\nexec \"$HERDR_LAST_TEST_BINARY\" -test.run=^TestHerdrFocusHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "event-complete")
	t.Setenv("HERDR_BIN_PATH", path)
	t.Setenv("HERDR_LAST_TEST_BINARY", binary)
	t.Setenv("HERDR_LAST_FOCUS_HELPER", "1")
	t.Setenv("HERDR_LAST_FOCUS_EVENT", eventName)
	t.Setenv("HERDR_LAST_FOCUS_WORKSPACE", workspace)
	t.Setenv("HERDR_LAST_FOCUS_TAB", tab)
	t.Setenv("HERDR_LAST_FOCUS_MARKER", marker)
	if breakStateAfterEvent {
		t.Setenv("HERDR_LAST_BREAK_STATE_AFTER_EVENT", "1")
	} else {
		t.Setenv("HERDR_LAST_BREAK_STATE_AFTER_EVENT", "")
	}
	return marker
}

func TestHerdrFocusHelperProcess(t *testing.T) {
	if os.Getenv("HERDR_LAST_FOCUS_HELPER") != "1" {
		return
	}
	args := []string{os.Getenv("HERDR_LAST_FOCUS_EVENT"), os.Getenv("HERDR_LAST_FOCUS_WORKSPACE")}
	if tab := os.Getenv("HERDR_LAST_FOCUS_TAB"); tab != "" {
		args = append(args, tab)
	}

	done := make(chan error, 1)
	go func() { done <- runEvent(args) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event hook could not acquire the state lock during focus")
	}

	if os.Getenv("HERDR_LAST_BREAK_STATE_AFTER_EVENT") == "1" {
		stateDir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
		if err := os.RemoveAll(stateDir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stateDir, []byte("block post-focus state persistence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(os.Getenv("HERDR_LAST_FOCUS_MARKER"), []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestToggleWorkspaceSurfacesPersistenceError(t *testing.T) {
	assertTogglePersistenceError(t, runToggleWorkspace)
}

func TestToggleTabSurfacesPersistenceError(t *testing.T) {
	assertTogglePersistenceError(t, runToggleTab)
}

func assertTogglePersistenceError(t *testing.T, toggle func() error) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	t.Setenv("HERDR_WORKSPACE_ID", "ws-1")
	t.Setenv("HERDR_TAB_ID", "tab-1")

	st := state.New()
	st.OnWorkspaceFocused("ws-seed")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if err := state.WithLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0700) })

	err := toggle()
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if !strings.Contains(err.Error(), "save") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommandsPreserveCorruptState(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "toggle workspace", run: runToggleWorkspace},
		{name: "toggle tab", run: runToggleTab},
		{name: "event", run: func() error { return runEvent([]string{"workspace.focused", "ws-1"}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := t.TempDir()
			statePath := filepath.Join(stateDir, "state.json")
			original := []byte("{corrupt\n")
			if err := os.WriteFile(statePath, original, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
			t.Setenv("HERDR_WORKSPACE_ID", "ws-1")
			t.Setenv("HERDR_TAB_ID", "tab-1")

			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "failed to load state") {
				t.Fatalf("error = %v, want state load error", err)
			}
			after, readErr := os.ReadFile(statePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, original) {
				t.Fatalf("corrupt state changed from %q to %q", original, after)
			}
		})
	}
}
