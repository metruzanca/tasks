# tasks

A terminal kanban board for you and your coding agent, with native Linear sync.

A `todo.md` works until two workers need it. Agents rewrite it, lose the
ordering, and cannot filter it; notes and research drown the actual work.
`tasks` gives you one structured board that both the TUI and an agent can use.
Keep it local or point it at the Linear team your startup already runs on. It
stores actionable project work, not notes, research, or conversation history.

## What it does

- Native Linear sync: workflow states become columns, and issues, labels,
  assignees, priorities, and comments stay in sync.
- Interactive terminal board, plus a scriptable CLI.
- Stable `--json` output so agents can read and update work without parsing prose.
- Statuses, priorities, labels, assignees, and comments.
- Local SQLite board by default, with GitHub Projects also supported.
- One board per repository by default, with no server or account required.

## Install

Requires Go. From a clone:

```sh
git clone https://github.com/metruzanca/tasks
cd tasks
go install .
```

Then configure a board:

```sh
tasks init
```

## Get started

```sh
tasks add "Fix expired session redirect" -d "Repro: ..." -p high -l bug
tasks next
tasks board
```

Inside a git repository, the default SQLite board lives in `.tasks/tasks.db` and
is kept out of git automatically.

## Linear

Already planning work in Linear? Connect a team and use the terminal against
your real issues:

```sh
tasks init --backend linear --team ENG
tasks login linear
tasks next
tasks board
```

Your team's workflow states become the columns. See
[docs/backends/linear.md](docs/backends/linear.md) for setup and details.

## Agents

Install the agent skill:

```sh
tasks skill install
```

The skill tells agents to read the board first, claim work, comment with
findings, and never delete tasks without asking. Agents get stable JSON:

```sh
tasks next --json
tasks move T-1 in_progress
tasks comment T-1 "Cause: cookie path mismatch"
```

```json
{
  "id": "T-1",
  "title": "Fix expired session redirect",
  "status": "in_progress",
  "priority": "high",
  "labels": ["bug"],
  "assignee": "you",
  "url": ""
}
```

IDs look like `T-12` for SQLite, `42` / `#42` for GitHub, and `ENG-123` for
Linear. Use the value printed by `add` or returned in JSON.

## Backends

Linear, GitHub Projects, and SQLite are native and compiled into the CLI.

| Backend | Use |
| --- | --- |
| Linear | Sync a team's issues and workflow states. [Docs](docs/backends/linear.md) |
| GitHub Projects | Sync issues to a GitHub project board. [Docs](docs/backends/github.md) |
| SQLite | Local board, default, no account needed. [Docs](docs/backends/sqlite.md) |

## Board

`tasks board` opens the TUI. Arrows navigate, `shift+arrows` move or reorder
cards, `enter` opens a task, `n` creates one, `f` filters, `r` refreshes, and
`q` quits. Mouse clicks, double-clicks, drag-and-drop, and scrolling work too.

## Commands

```text
tasks add "title" [-d desc] [-s status] [-p priority] [-l label] [-a @me]
tasks list [--status s] [--label l] [--priority p] [--search text] [--all]
tasks show <id>
tasks edit <id> [--title t] [--description d] [--priority p] [--assignee a]
tasks move <id> <status> [--before <id> | --after <id>]
tasks done <id>
tasks next
tasks comment <id> "text"
tasks comments <id>
tasks archive <id>
tasks rm <id> [--hard --yes]
tasks columns
tasks board
tasks login [backend]
tasks logout [backend]
tasks plugin list|install|remove|path
tasks init|doctor|config|skill|version
```

Global flags: `--backend <name>`, `--json`, `--no-color`, `--verbose`. Run
`tasks --help` or `tasks <command> --help` for every option.

## Configuration and credentials

Global config lives at `~/.config/tasks/config.toml`. A `.tasks.toml` in a
repository overrides it. Precedence is environment, repo config, global config,
defaults.

Tokens are stored separately so config can be committed:

```text
~/.local/share/tasks/credentials.toml
```

Use `tasks login <backend>` and `tasks logout <backend>`. `TASKS_CREDENTIALS_FILE`
overrides the path, and CI tokens such as `GH_TOKEN` and `LINEAR_API_KEY` still
take precedence. See the backend docs and `tasks config show` for details.

## External plugins

Native backends are bundled. Other backends are separate `tasks-plugin-<name>`
executables and can be installed with `tasks plugin install`. See
[docs/plugins.md](docs/plugins.md) for discovery, installation, and how to write
one.

## Development

Mise is for development only:

```sh
mise run build
mise run test
mise run lint
mise run generate
mise run fmt
```

Mise installs the pinned Go toolchain, `buf`, and the protobuf plugins from
`mise.toml` on first use.

Layout:

```text
internal/cli        CLI commands and TUI
internal/native     native backend registry
internal/config     config merge and paths
internal/pluginmgr  plugin discovery and client
pkg/sdk             public plugin contract
plugins/*           backend implementations
proto/backend.proto plugin RPC definition
docs/                backend and plugin documentation
```
