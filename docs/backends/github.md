# GitHub Projects backend

The GitHub backend is an external plugin. Tasks are issues in one repository,
added to a Projects v2 board. Labels, assignees, comments, descriptions, and
priority are supported.

## Setup

Install the plugin:

```sh
tasks plugin install github
```

For local development, build it next to the `tasks` binary or into the plugin
directory:

```sh
mise run plugin-github
cp bin/tasks-plugin-github "${XDG_DATA_HOME:-$HOME/.local/share}/tasks/plugins/"
tasks doctor
```

Authenticate with the `project` and `repo` scopes. The plugin uses `GH_TOKEN`,
`GITHUB_TOKEN`, or the token from `gh auth token`.

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

- Manual ordering within a column is not supported, so `J`/`K` are disabled in
  the TUI and `move --before`/`--after` returns an error.
- Draft issues and pull requests already on the board are ignored.
- GitHub does not allow issue deletion, so removing a task archives and closes
  it instead.
