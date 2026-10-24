# tasks

A terminal kanban board for humans and agents. Humans can use the interactive
board or the CLI; agents get stable JSON commands so the board can be their
project todo list instead of a `todo.md` file.

`tasks` is not agent memory. It stores actionable project work, not notes,
research dumps, or conversation history.

The CLI host is backend-agnostic. Storage and sync live in
[hashicorp/go-plugin](https://github.com/hashicorp/go-plugin) backends over gRPC.
The bundled SQLite backend works out of the box; other backends are installed as
`tasks-plugin-<name>` executables.

## Backends

| Backend | Delivery | Docs |
| --- | --- | --- |
| SQLite | Built in, default | [docs/backends/sqlite.md](docs/backends/sqlite.md) |
| GitHub Projects | External plugin | [docs/backends/github.md](docs/backends/github.md) |

```sh
tasks plugin list
tasks plugin install github
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

`j`/`k` move, `h`/`l` switch columns, `H`/`L` move a task between columns,
`J`/`K` reorder (when the backend supports it), `n` new, `e` edit description,
`enter` detail, `d` done, `a` archive, `/` filter, `r` refresh, `q` quit.

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

Backend-specific environment variables are documented with each backend.

`tasks config path` and `tasks config show` explain what is active.
`tasks doctor` checks config, plugin discovery, and backend connectivity.

## Plugins

```sh
tasks plugin list
tasks plugin install <name>            # go install the official plugin module
tasks plugin install <module>@<version> --name <name>
tasks plugin remove <name>
tasks plugin path <name>
```

Plugins are discovered as `tasks-plugin-<name>` in this order:

1. `plugin_dir` from config
2. `~/.local/share/tasks/plugins`
3. next to the `tasks` executable
4. `PATH`

`plugin install` runs `go install` with `GOBIN` set to the plugin directory, then
starts the plugin and calls `Health` to verify it. The module must be published
for `go install` to work.

For local development, build the plugin into the plugin directory:

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
mise run build       # bin/tasks and bin/tasks-plugin-github
mise run test
mise run lint
mise run generate    # buf + protoc plugins are managed by mise
mise run install     # go install the CLI and GitHub plugin
mise run fmt
mise run clean
```

Mise installs the pinned Go toolchain, `buf`, and the protobuf plugins from
`mise.toml` on first use.

Layout:

```text
cmd/tasks                     host CLI + bundled backend server
cmd/tasks-plugin-github       external GitHub plugin
internal/cli                  commands, output, init/doctor
internal/cli/tui              Bubble Tea board
internal/config               config merge and paths
internal/pluginmgr            discovery, go-plugin client, install
pkg/sdk                       public plugin contract
plugins/*                     backend implementations
proto/backend.proto           plugin RPC definition
skills/tasks/SKILL.md         agent workflow
docs/backends/                backend documentation
```
