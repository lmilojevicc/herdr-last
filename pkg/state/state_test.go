package state

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStateTransitions(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	s := New()

	if !s.OnTabFocused("ws1", "tab1") {
		t.Error("expected state change on initial tab focus")
	}
	if s.GetCurrentWorkspace() != "ws1" {
		t.Fatalf("expected current workspace ws1, got %s", s.GetCurrentWorkspace())
	}
	if !s.OnTabFocused("ws1", "tab2") {
		t.Error("expected state change on focus tab2")
	}
	if prev := s.GetPreviousTab("ws1"); prev != "tab1" {
		t.Fatalf("expected previous tab tab1, got %s", prev)
	}
	if !s.OnTabFocused("ws1", "tab3") {
		t.Error("expected state change on focus tab3")
	}
	if prev := s.GetPreviousTab("ws1"); prev != "tab2" {
		t.Fatalf("expected previous tab tab2, got %s", prev)
	}
	if !s.OnWorkspaceFocused("ws2") {
		t.Error("expected state change on workspace switch")
	}
	if prevWS := s.GetPreviousWorkspace(); prevWS != "ws1" {
		t.Fatalf("expected previous ws ws1, got %s", prevWS)
	}

	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s2.GetPreviousWorkspace() != "ws1" {
		t.Fatalf("reloaded previous workspace = %s, want ws1", s2.GetPreviousWorkspace())
	}
	if prevTab := s2.GetPreviousTab("ws1"); prevTab != "tab2" {
		t.Fatalf("reloaded previous tab = %s, want tab2", prevTab)
	}
}

func TestWithLockPreventsLostUpdatesAcrossProcesses(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	attemptedPath := filepath.Join(t.TempDir(), "attempted")
	enteredPath := filepath.Join(t.TempDir(), "entered")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWithLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		"HERDR_LAST_LOCK_HELPER=1",
		"HERDR_LAST_LOCK_ATTEMPTED="+attemptedPath,
		"HERDR_LAST_LOCK_ENTERED="+enteredPath,
	)
	var childOutput bytes.Buffer
	cmd.Stdout = &childOutput
	cmd.Stderr = &childOutput
	var childErr error
	childDone := make(chan struct{})

	err := WithLock(func() error {
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() {
			childErr = cmd.Wait()
			close(childDone)
		}()

		if err := waitForPath(attemptedPath, 2*time.Second); err != nil {
			return err
		}
		select {
		case <-childDone:
			return fmt.Errorf("helper exited before the parent released the lock: %v (%s)", childErr, childOutput.String())
		case <-time.After(150 * time.Millisecond):
		}
		if _, err := os.Stat(enteredPath); !os.IsNotExist(err) {
			return fmt.Errorf("helper entered the transaction while the parent held the lock")
		}

		st, err := Load()
		if err != nil {
			return err
		}
		st.OnWorkspaceFocused("ws-1")
		return st.Save()
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-childDone:
		if childErr != nil {
			t.Fatalf("helper failed: %v (%s)", childErr, childOutput.String())
		}
	case <-ctx.Done():
		t.Fatal("helper did not acquire the released lock")
	}
	if _, err := os.Stat(enteredPath); err != nil {
		t.Fatalf("helper never entered the transaction: %v", err)
	}

	st, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.GetCurrentWorkspace() != "ws-1" || st.CurrentTabPerWS["ws-1"] != "tab-1" {
		t.Fatalf("serialized updates were lost: %#v", st)
	}
}

func TestWithLockHelperProcess(t *testing.T) {
	if os.Getenv("HERDR_LAST_LOCK_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("HERDR_LAST_LOCK_ATTEMPTED"), []byte("attempting"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WithLock(func() error {
		if err := os.WriteFile(os.Getenv("HERDR_LAST_LOCK_ENTERED"), []byte("entered"), 0600); err != nil {
			return err
		}
		st, err := Load()
		if err != nil {
			return err
		}
		st.OnTabFocused("ws-1", "tab-1")
		return st.Save()
	}); err != nil {
		t.Fatal(err)
	}
}

func waitForPath(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", path)
}
