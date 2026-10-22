# tasks

A terminal kanban board for humans and agents. Humans can use the interactive
board or the CLI; agents get stable JSON commands so the board can be their
project todo list instead of a `todo.md` file.

`tasks` is not agent memory. It stores actionable project work, not notes,
research dumps, or conversation history.

- Default backend: local SQLite (no server, no account).
- Optional backend: GitHub Projects v2, where tasks are issues in a repo.
- Backends are [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)
  plugins over gRPC. SQLite ships inside the `tasks` binary; other backends are
  installed as `tasks-plugin-<name>` executables.

## Quick start

```sh
make build
export PATH="$PWD/bin:$PATH"

tasks init
tasks add "Fix expired session redirect" -d "Repro: ..." -p high -l bug
tasks list
tasks move T-1 in_progress
tasks comment T-1 "Cause: cookie path mismatch"
tasks done T-1
tasks board
```

The SQLite database lives at `~/.local/share/tasks/tasks.db` by default. Config
lives at `~/.config/tasks/config.toml`.

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

IDs are `T-12` for SQLite and issue numbers (`42`, `#42`) for GitHub. Commands
accept either form.

JSON output is stable and compact enough to pipe:

```json
{
  "id": "T-1",
  "key": "T-1",
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

## GitHub Projects

Tasks are issues in one repository, added to a Projects v2 board. Labels,
assignees, comments, descriptions, and priority are supported.

1. Authenticate with the `project` and `repo` scopes. The CLI uses `GH_TOKEN`,
   `GITHUB_TOKEN`, or `gh auth token`.
2. Configure the board:

```sh
tasks init --backend github \
  --owner metruzanca \
  --project 4 \
  --repo metruzanca/tasks
```

`init` validates access to the project and creates a `Priority` single-select
field with `None`, `Low`, `Medium`, `High`, `Urgent` options if it is missing.
Set `--status-field` or `--priority-field` if your board uses different names.
The `Status` field must already exist and be single-select.

Limitations:

- Manual ordering within a column is not supported (`J`/`K` are disabled).
- Draft issues and pull requests on the board are ignored.
- Closing/archiving a task closes the issue; GitHub does not allow issue deletion.

## Configuration

Global config:

```toml
backend = "sqlite"

[sqlite]
path = "~/.local/share/tasks/tasks.db"
me = "metruzanca"
ready_status = "todo"

[github]
owner = "metruzanca"
project = 4
repo = "metruzanca/tasks"
status_field = "Status"
priority_field = "Priority"
# token_env = "MY_GITHUB_TOKEN"
```

A `.tasks.toml` in a repository overrides the global config for commands run
under that directory. Precedence is environment, repo config, global config,
defaults. Environment overrides: `TASKS_BACKEND`, `TASKS_PLUGIN_DIR`,
`TASKS_SQLITE_PATH`, `TASKS_READY_STATUS`.

`tasks config path` and `tasks config show` explain what is active.
`tasks doctor` checks config, plugin discovery, and backend connectivity.

## Plugins

```sh
tasks plugin list
tasks plugin install github            # go install .../cmd/tasks-plugin-github@latest
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
for `go install` to work. For local development:

```sh
go build -o "${XDG_DATA_HOME:-$HOME/.local/share}/tasks/plugins/tasks-plugin-github" \
  ./cmd/tasks-plugin-github
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
make build       # bin/tasks and bin/tasks-plugin-github
make test
make lint
make generate    # requires Go; installs buf and protoc plugins into .tools/
```

Layout:

```text
cmd/tasks                     host CLI + hidden "__plugin sqlite" server
cmd/tasks-plugin-github       external GitHub plugin
internal/cli                  commands, output, init/doctor
internal/cli/tui              Bubble Tea board
internal/config               config merge and paths
internal/pluginmgr            discovery, go-plugin client, install
pkg/sdk                       public plugin contract
plugins/sqlite                built-in SQLite backend
plugins/github                GitHub Projects backend
proto/backend.proto           plugin RPC definition
skills/tasks/SKILL.md         agent workflow
```

SQLite concurrency uses WAL, `busy_timeout`, and foreign keys, so a human and
several agents can use the same board.
