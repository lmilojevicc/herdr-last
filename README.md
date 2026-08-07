<div align='center'>

# herdr-last

<p align="center">
    <em>Check out my other Herdr-related projects:</em> <br>
    <a href="https://github.com/lmilojevicc/seshagy"><em>Seshagy - Agent aware TUI session manager</em></a>
    &nbsp; | &nbsp;
    <a href="https://github.com/lmilojevicc/herdr-splits.nvim"><em>Herdr splits - Seamless navigation between Neovim splits and Herdr panes</em></a>
    &nbsp; | &nbsp;
    <a href="https://github.com/lmilojevicc/herdr-tab-rename"><em>Herdr tab rename - Autorename tabs for Herdr</em></a>
</p>

</div>

`herdr-last` is an open-source plugin for [Herdr](https://herdr.dev) written in Go. It enables `tmux`-like instant toggling to your **previously active workspace** and **previously active tab**.

---

## Features

- **Toggle Last Workspace**: Instantly jump back and forth between your two most recent workspaces (`tmux switch-client -l` equivalent).
- **Toggle Last Tab**: Jump back and forth between your two most recent tabs in the current workspace (`tmux select-window -l` equivalent).
- **Zero External Dependencies**: Single lightweight Go binary built with standard library networking and atomic state management.
- **Cross-Platform**: Automated prebuilt release binaries for macOS (`arm64`/`amd64`) and Linux (`amd64`/`arm64`).

---

## Installation

### From GitHub

Install directly using Herdr's plugin manager:

```bash
herdr plugin install lmilojevicc/herdr-last
```

Herdr will clone the repository, run `scripts/install.sh`, download and verify the release declared by `herdr-plugin.toml` for your architecture, and register the plugin. If the release cannot be retrieved or verified, the installer falls back to a local Go build when Go is available.

### Local Development

`herdr plugin link` does not run build hooks, so build the path declared by the manifest before linking a local clone:

```bash
git clone https://github.com/lmilojevicc/herdr-last.git ~/Projects/herdr-last
cd ~/Projects/herdr-last
mkdir -p bin
go build -o bin/herdr-last .
herdr plugin link ~/Projects/herdr-last
```

---

## Keybinding Configuration

Add the following plugin actions to your `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+,"
type = "plugin_action"
command = "herdr-last.toggle-workspace"
description = "Toggle to last workspace"

[[keys.command]]
key = "prefix+."
type = "plugin_action"
command = "herdr-last.toggle-tab"
description = "Toggle to last tab"
```

Then reload Herdr's configuration:

```bash
herdr server reload-config
```

---

## Commands & Usage

* `herdr-last toggle-workspace`: Switches focus to the previously active workspace.
* `herdr-last toggle-tab`: Switches focus to the previously active tab within the active workspace.
* `herdr-last daemon`: Runs background event listener connected to Herdr's Unix domain socket (`HERDR_SOCKET_PATH` or `~/.config/herdr/herdr.sock`).
* `herdr-last event <event_name>`: Event hook handler called on `workspace.focused` or `tab.focused`.
* `herdr-last version`: Prints current version.

---

## License

MIT
