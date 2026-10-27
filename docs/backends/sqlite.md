# SQLite backend

The SQLite backend is built into the `tasks` binary and is the default. It needs
no server, account, or plugin installation.

## Storage

Inside a git repository, the board is stored at `<repo>/.tasks/tasks.db`. The
`.tasks` directory is created with a `.gitignore` containing `*`, so board data
never appears in `git status`.

Outside a repository, the board falls back to
`~/.local/share/tasks/tasks.db`.

Each repository gets an isolated board by default. A global `[sqlite] path` in
`~/.config/tasks/config.toml` is only used outside a repository.

`tasks board --web` is not available because SQLite boards have no hosted web
view.

## Setup

```sh
tasks init
tasks task add "Fix expired session redirect" -p high -l bug
tasks board
```

To use a specific database file:

```sh
tasks init --backend sqlite --db ~/boards/personal.db
```

## Sharing a board

Point both repositories at the same database with an explicit path in each
repository's `.tasks.toml`:

```toml
backend = "sqlite"

[sqlite]
path = "~/.local/share/tasks/shared.db"
```

Without an explicit path, every repository's board is isolated.

## Configuration

```toml
backend = "sqlite"

[sqlite]
# Omit to use the repository-local or global default.
path = "~/.local/share/tasks/shared.db"
me = "metruzanca"
ready_status = "todo"
```

- `path`: explicit database path. Also enables sharing between repositories.
- `me`: value used for `--assignee @me` and as the comment author.
- `ready_status`: column used by `tasks task next`. Defaults to `todo`.

Environment overrides:

- `TASKS_SQLITE_PATH`: override the database path.
- `TASKS_READY_STATUS`: override the ready column.

## Behavior

Default columns are `backlog`, `todo`, `in_progress`, `in_review`, and `done`.
Priorities are `none`, `low`, `medium`, `high`, and `urgent`.

- Tasks are ordered within a column, and `move` supports `--before` and `--after`.
- Labels, assignees, descriptions, and comments are stored locally.
- `archive` hides a task but keeps its data. `rm --hard --yes` permanently
  deletes it.
- SQLite uses WAL, a busy timeout, and foreign keys, so a human and several
  agents can use the same board concurrently.
