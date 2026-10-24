# tasks

A terminal kanban board for humans and agents. Humans can use the interactive
board or the CLI; agents get stable JSON commands so the board can be their
project todo list instead of a `todo.md` file.

`tasks` is not agent memory. It stores actionable project work, not notes,
research dumps, or conversation history.

The CLI host is backend-agnostic. Storage and sync live in
[hashicorp/go-plugin](https://github.com/hashicorp/go-plugin) backends over gRPC.
SQLite, GitHub Projects, and Linear are native backends compiled into the CLI.
Other backends are installed as `tasks-plugin-<name>` executables.

## Backends

| Backend | Delivery | Docs |
| --- | --- | --- |
| SQLite | Native, default | [docs/backends/sqlite.md](docs/backends/sqlite.md) |
| GitHub Projects | Native | [docs/backends/github.md](docs/backends/github.md) |
| Linear | Native | [docs/backends/linear.md](docs/backends/linear.md) |

```sh
tasks plugin list
```

## Quick start

```sh
mise run build
export PATH="$PWD/bin:$PATH"

tasks init
tasks add "Fix expired session redirect" -d "Repro: ..." -p high -l bug
tasks list
tasks move <id> in_progress
tasks comment <id> "Cause: cookie path mismatch"
tasks done <id>
tasks board
```

Configuration lives at `~/.config/tasks/config.toml`. A `.tasks.toml` in a
repository overrides it. See the backend docs for storage, setup, and
backend-specific settings.

## Agent skill

```sh
tasks skill install          # ~/.config/opencode/skills/tasks/SKILL.md
tasks skill print            # print it for another agent runtime
```

The skill tells agents to read the board first, avoid duplicates, claim work,
comment with findings, and never delete tasks without the user asking. A copy
lives in `skills/tasks/SKILL.md`.

## Commands

```text
tasks add "title" [-d desc] [-s status] [-p priority] [-l label] [--assignee @me]
tasks list [--status s] [--label l] [--priority p] [--assignee a] [--search text] [--all]
tasks show <id>
tasks edit <id> [--title t] [--description d] [--priority p] [--label l] [--assignee a] [--clear-assignee]
tasks move <id> <status> [--before <id> | --after <id>]
tasks done <id>
tasks next
tasks comment <id> "text"        # omit text or use "-" to read stdin
tasks comments <id>
tasks login [backend]           # store credentials, never in config
tasks logout [backend]
tasks archive <id>
tasks rm <id> [--hard --yes]
tasks columns
tasks board
tasks plugin list|install|remove|path
tasks init|doctor|config|skill|version
```

Global flags: `--backend <name>`, `--json`, `--no-color`, `--verbose`.

IDs are provided by the backend. Use the value printed by `add` or returned in
JSON; commands also accept the backend's display key.

JSON output is stable and compact enough to pipe:

```json
{
  "id": "task-1",
  "key": "task-1",
  "title": "Fix expired session redirect",
  "description": "Repro: ...",
  "status": "todo",
  "priority": "high",
  "labels": ["bug"],
  "assignee": "metru",
  "url": "",
  "created_at": "2026-10-21T01:42:31Z",
  "updated_at": "2026-10-21T01:43:10Z",
  "archived": false,
  "comment_count": 1
}
```

## Board keys

Arrow keys navigate: `←`/`→` switch columns and `↑`/`↓` select cards.
`shift+←`/`shift+→` move the selected card between columns, and
`shift+↑`/`shift+↓` reorder it when the backend supports ordering.

With a mouse, click a card to select it, double-click to open it, drag it onto
another column to move it, and use the wheel to scroll through a column.

`enter` opens the task screen, `n` new, `f` or `/` filter, `r` refresh, and
`q` quit. The task screen shows details alongside editable title, description,
labels, status, priority, and assignee fields. Use `tab`/`shift+tab` between
fields and the Save/Delete/Close buttons, or click them with the mouse. Esc or
Close leaves without saving. Delete asks for confirmation before removing the
task.

## Configuration

Global configuration is read from `~/.config/tasks/config.toml`. The nearest
`.tasks.toml` in a repository overrides it. Precedence is environment, repo
config, global config, defaults.

```toml
backend = "<name>"
plugin_dir = "~/.local/share/tasks/plugins"
# Backend-specific sections are documented in docs/backends/.
```

Generic environment overrides:

- `TASKS_BACKEND`: backend name.
- `TASKS_PLUGIN_DIR`: plugin discovery directory.
- `TASKS_CREDENTIALS_FILE`: credentials file path.

Backend-specific environment variables are documented with each backend.

`tasks config path` and `tasks config show` explain what is active.
`tasks doctor` checks config, plugin discovery, and backend connectivity.

## Credentials

Tokens are stored separately from configuration so the config can be committed:

```text
~/.local/share/tasks/credentials.toml
```

The file is created with `0600` permissions in a `0700` directory. Run
`tasks login <backend>` to store a token and `tasks logout <backend>` to remove
it. `TASKS_CREDENTIALS_FILE` overrides the location. Environment variables such
as `GH_TOKEN` and `LINEAR_API_KEY` still take precedence, which is the intended
path for CI.

## Plugins

Native backends (`sqlite`, `github`, `linear`) are compiled into the `tasks`
binary and need no install. External backends are separate
`tasks-plugin-<name>` executables.

```sh
tasks plugin list
tasks plugin install <module>@<version> --name <name>
tasks plugin remove <name>
tasks plugin path <name>
```

External plugins are discovered as `tasks-plugin-<name>` in this order:

1. `plugin_dir` from config
2. `~/.local/share/tasks/plugins`
3. next to the `tasks` executable
4. `PATH`

`plugin install` runs `go install` with `GOBIN` set to the plugin directory, then
starts the plugin and calls `Health` to verify it. The module must be published
for `go install` to work. Native names cannot be installed or removed.

For local development of an external plugin, build it into the plugin directory:

```sh
go build -o "${XDG_DATA_HOME:-$HOME/.local/share}/tasks/plugins/tasks-plugin-<name>" \
  ./cmd/tasks-plugin-<name>
tasks doctor
```

### Writing a backend

1. Implement `sdk.Backend` (`pkg/sdk/backend.go`).
2. Parse your section from the JSON in `TASKS_PLUGIN_CONFIG`.
3. Call `sdk.Serve(backend)` from `main`.
4. Name the binary `tasks-plugin-<name>`.

`Describe` advertises columns, capabilities, and the ready column. The host
renders tables, JSON, and the TUI, so a plugin only needs persistence semantics.
The protocol is defined in `proto/backend.proto`; generated stubs are committed.

## Development

```sh
mise run build       # bin/tasks
mise run test
mise run lint
mise run generate    # buf + protoc plugins are managed by mise
mise run install     # go install the CLI
mise run fmt
mise run clean

go install .         # installs the tasks binary from the root package
```

Mise installs the pinned Go toolchain, `buf`, and the protobuf plugins from
`mise.toml` on first use.

Layout:

```text
main.go                       host CLI + native backend server
cmd/tasks-plugin-github       optional standalone GitHub plugin
cmd/tasks-plugin-linear       optional standalone Linear plugin
internal/cli                  commands, output, init/doctor
internal/native               native backend registry and server
internal/cli/tui              Bubble Tea board
internal/config               config merge and paths
internal/pluginmgr            discovery, go-plugin client, install
pkg/sdk                       public plugin contract
plugins/*                     backend implementations
proto/backend.proto           plugin RPC definition
skills/tasks/SKILL.md         agent workflow
docs/backends/                backend documentation
```
