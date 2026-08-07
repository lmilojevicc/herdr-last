package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"herdr-last/pkg/client"
	"herdr-last/pkg/state"
)

const Version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "daemon":
		runDaemon()
	case "toggle-workspace":
		if err := runToggleWorkspace(); err != nil {
			log.Fatal(err)
		}
	case "toggle-tab":
		if err := runToggleTab(); err != nil {
			log.Fatal(err)
		}
	case "event":
		if err := runEvent(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "version", "-v", "--version":
		fmt.Printf("herdr-last v%s\n", Version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`herdr-last - Toggle previously active tab or workspace in Herdr

Usage:
  herdr-last <command> [arguments]

Commands:
  daemon            Start event-listener daemon
  toggle-workspace  Switch to the previously active workspace
  toggle-tab        Switch to the previously active tab in current workspace
  event [name]      CLI event handler fallback (workspace.focused | tab.focused)
  version           Print version information
  help              Show this help message`)
}

func loadState() (*state.State, error) {
	st, err := state.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}
	return st, nil
}

func runDaemon() {
	log.Printf("Starting herdr-last daemon v%s...", Version)
	c := client.New()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	events := []string{"workspace.focused", "tab.focused"}

	go func() {
		for {
			log.Printf("Connecting to Herdr socket (%s)...", c.SocketPath())
			err := c.Subscribe(events, func(evt client.Event) {
				if err := state.WithLock(func() error {
					st, err := loadState()
					if err != nil {
						return err
					}
					var changed bool
					switch evt.Name {
					case "workspace.focused":
						changed = st.OnWorkspaceFocused(evt.WorkspaceID)
					case "tab.focused":
						changed = st.OnTabFocused(evt.WorkspaceID, evt.TabID)
					}
					if changed {
						return st.Save()
					}
					return nil
				}); err != nil {
					log.Printf("Error updating state: %v", err)
				}
			})
			if err != nil {
				log.Printf("Socket subscription disconnected: %v. Reconnecting in 3s...", err)
			}
			time.Sleep(3 * time.Second)
		}
	}()

	<-sigChan
	log.Println("Shutting down herdr-last daemon...")
}

func runToggleWorkspace() error {
	currentWS := os.Getenv("HERDR_WORKSPACE_ID")
	currentTab := os.Getenv("HERDR_TAB_ID")
	var prevWS string

	if err := state.WithLock(func() error {
		st, err := loadState()
		if err != nil {
			return err
		}
		if currentWS != "" {
			st.OnWorkspaceFocused(currentWS)
			if currentTab != "" {
				st.OnTabFocused(currentWS, currentTab)
			}
		}
		prevWS = st.GetPreviousWorkspace()
		if err := st.Save(); err != nil {
			return fmt.Errorf("failed to save workspace history: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	if prevWS == "" || prevWS == currentWS {
		fmt.Println("No previous workspace recorded yet.")
		return nil
	}
	if err := client.New().FocusWorkspace(prevWS); err != nil {
		return fmt.Errorf("failed to focus workspace %q: %w", prevWS, err)
	}

	if err := state.WithLock(func() error {
		st, err := loadState()
		if err != nil {
			return err
		}
		if st.OnWorkspaceFocused(prevWS) {
			if err := st.Save(); err != nil {
				return fmt.Errorf("focused workspace %q but failed to save history: %w", prevWS, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("Switched to workspace: %s\n", prevWS)
	return nil
}

func runToggleTab() error {
	currentWS := os.Getenv("HERDR_WORKSPACE_ID")
	currentTab := os.Getenv("HERDR_TAB_ID")
	var prevTab string

	if err := state.WithLock(func() error {
		st, err := loadState()
		if err != nil {
			return err
		}
		if currentWS == "" {
			currentWS = st.GetCurrentWorkspace()
		}
		if currentWS != "" && currentTab != "" {
			st.OnTabFocused(currentWS, currentTab)
		}
		if currentWS == "" {
			return fmt.Errorf("current workspace ID unknown")
		}
		prevTab = st.GetPreviousTab(currentWS)
		if err := st.Save(); err != nil {
			return fmt.Errorf("failed to save tab history: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	if prevTab == "" || prevTab == currentTab {
		fmt.Printf("No previous tab recorded for workspace '%s'.\n", currentWS)
		return nil
	}
	if err := client.New().FocusTab(currentWS, prevTab); err != nil {
		return fmt.Errorf("failed to focus tab %q in workspace %q: %w", prevTab, currentWS, err)
	}

	if err := state.WithLock(func() error {
		st, err := loadState()
		if err != nil {
			return err
		}
		if st.OnTabFocused(currentWS, prevTab) {
			if err := st.Save(); err != nil {
				return fmt.Errorf("focused tab %q but failed to save history: %w", prevTab, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("Switched to tab '%s' in workspace '%s'\n", prevTab, currentWS)
	return nil
}

func resolveEvent(args []string) (eventName, wsID, tabID string) {
	eventName = os.Getenv("HERDR_PLUGIN_EVENT")
	wsID = os.Getenv("HERDR_WORKSPACE_ID")
	tabID = os.Getenv("HERDR_TAB_ID")

	if raw := os.Getenv("HERDR_PLUGIN_EVENT_JSON"); raw != "" {
		if evt, err := client.DecodeEvent([]byte(raw)); err == nil {
			if eventName == "" {
				eventName = evt.Name
			}
			if wsID == "" {
				wsID = evt.WorkspaceID
			}
			if tabID == "" {
				tabID = evt.TabID
			}
		}
	}

	if len(args) > 0 && args[0] != "" {
		eventName = args[0]
	}
	if len(args) > 1 && args[1] != "" {
		wsID = args[1]
	}
	if len(args) > 2 && args[2] != "" {
		tabID = args[2]
	}
	return eventName, wsID, tabID
}

func runEvent(args []string) error {
	eventName, wsID, tabID := resolveEvent(args)
	if eventName == "" {
		return fmt.Errorf("event name required (via argument or HERDR_PLUGIN_EVENT)")
	}

	return state.WithLock(func() error {
		st, err := loadState()
		if err != nil {
			return err
		}
		var changed bool
		switch eventName {
		case "workspace.focused":
			if wsID == "" {
				return fmt.Errorf("workspace ID required for workspace.focused event")
			}
			changed = st.OnWorkspaceFocused(wsID)
		case "tab.focused":
			if wsID == "" || tabID == "" {
				return fmt.Errorf("workspace ID and tab ID required for tab.focused event")
			}
			changed = st.OnTabFocused(wsID, tabID)
		default:
			return fmt.Errorf("unsupported event name: %s", eventName)
		}

		if changed {
			if err := st.Save(); err != nil {
				return fmt.Errorf("failed to save event history: %w", err)
			}
		}
		return nil
	})
}
