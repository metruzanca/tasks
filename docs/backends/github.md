# GitHub Projects backend

The GitHub backend is native and compiled into the `tasks` binary. Tasks are
issues in one repository, added to a Projects v2 board. Labels, assignees,
comments, descriptions, and priority are supported.

## Setup

Authenticate with the `project` and `repo` scopes:

```sh
tasks login github
```

Login uses the token from `gh auth token` when the prompt is left blank, or
accepts a pasted personal access token. Tokens are stored in
`~/.local/share/tasks/credentials.toml`, never in config. The plugin also uses
`GH_TOKEN`, `GITHUB_TOKEN`, `token_env`, or `gh auth token` when no stored token
exists.

Configure the board:

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

## Configuration

```toml
backend = "github"

[github]
owner = "metruzanca"
project = 4
repo = "metruzanca/tasks"
status_field = "Status"
priority_field = "Priority"
# token_env = "MY_GITHUB_TOKEN"
```

- `owner`: user or organization that owns the project.
- `project`: Projects v2 project number.
- `repo`: repository that receives the issues, as `owner/name`.
- `status_field`: single-select field used as the kanban column.
- `priority_field`: single-select field used for priority.
- `token_env`: optional environment variable to read the token from instead of
  the defaults.

## Mapping

- Task ID: issue number. The display key is `#<number>`.
- Board column: the value of the `Status` single-select field.
- Priority: the value of the `Priority` single-select field.
- Description: issue body.
- Labels, assignee, and comments map to the issue's labels, assignee, and
  comments.
- `archive` archives the project item and closes the issue.

## Limitations

- Manual ordering within a column is not supported, so `shift+↑`/`shift+↓`
  reordering is disabled in the TUI and `move --before`/`--after` returns an
  error.
- Draft issues and pull requests already on the board are ignored.
- GitHub does not allow issue deletion, so removing a task archives and closes
  it instead.
