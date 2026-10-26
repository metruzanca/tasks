# Linear backend

The Linear backend is native and compiled into the `tasks` binary. Tasks are
issues in one team, and the team's workflow states are the board columns.
Labels, assignees, comments, descriptions, priority, archive, and manual
ordering are supported.

## Login

```sh
tasks auth login linear
tasks auth logout linear
```

Interactive `tasks init` also starts this login flow when you choose Linear. If
you are already logged in, it lists your teams so you can pick one instead of
typing the key. Credentials are stored in `~/.local/share/tasks/credentials.toml` with `0600`
permissions, never in `config.toml` or `.tasks.toml`. Set
`TASKS_CREDENTIALS_FILE` to use another path.

Login tries OAuth first when an OAuth client ID is configured. Otherwise it asks
for a personal API key. Create a key at Linear Settings > Security & access >
Personal API keys. For CI, set `LINEAR_API_KEY` instead of storing credentials.

### OAuth

Linear supports OAuth 2.0 Authorization Code with PKCE and does not require a
client secret for PKCE. It does not support dynamic client registration or a
device flow, so a browser login needs a registered public OAuth app with this
exact redirect URI:

```text
http://127.0.0.1:43172/callback
```

The plugin bundles a public client ID, so browser login works without any
extra setup. It uses a fixed loopback callback, and the app is public so other
workspaces can authorize it.

To use your own OAuth app instead, create one at
<https://linear.app/settings/api/applications/new>, enable public distribution
if other workspaces should install it, and copy the client ID. Then configure
it:

```sh
tasks init --backend linear --team ENG --oauth-client-id <client-id>
```

or set `LINEAR_OAUTH_CLIENT_ID`. A client ID is public and safe to commit. The
plugin never asks for or stores a client secret, because PKCE does not need
one.

Access tokens expire after about 24 hours. The plugin refreshes them with the
rotating refresh token and saves the new pair atomically. If refresh fails, run
`tasks auth login linear` again.

`tasks auth logout linear` removes the local token. It does not revoke the grant in
Linear. Revoke the application from Linear Settings > Applications if needed.

## Configuration

```toml
backend = "linear"

[linear]
team = "ENG"
# oauth_client_id = "..."
# token_env = "MY_LINEAR_TOKEN"
# ready_status = "Todo"
```

- `team`: team key (`ENG`) or UUID. Required.
- `oauth_client_id`: public OAuth app client ID. Optional. Set it to `none`
  or `off` to skip browser OAuth and use a personal API key instead.
- `token_env`: optional environment variable to read a token from instead of
  the credentials file.
- `ready_status`: optional workflow state name to use as the ready column.

```sh
tasks init --backend linear --team ENG --write repo
```

`init` validates team access and creates nothing in Linear.

## Mapping

- Task ID: the issue UUID. The display key is the identifier, such as `ENG-123`.
  Commands accept either.
- Board column: the issue's workflow state.
- Column categories: `backlog`, `unstarted`, and `triage` map to todo;
  `started` maps to in progress; `completed`, `canceled`, and `duplicate` map to
  done.
- Priority: Linear `0/1/2/3/4` maps to none/urgent/high/medium/low.
- Description: issue description in Markdown.
- Labels, assignee, comments, and URL map to the issue's labels, assignee,
  comments, and URL.
- Position: Linear's manual `sortOrder`. `tasks task move --before/--after` and the
  TUI reorder keys compute a fractional order between neighbors.
- `tasks task archive` archives the issue. `tasks task rm --hard --yes` permanently deletes
  it with `issueDelete`.

## Limitations

- Labels must already exist in the team. Creating a task with an unknown label
  returns an error unless you create the label first.
- The board is one team. Projects and cycles are not used for scoping.
- The workflow state list and team members are read in single pages of up to
  250 entries.
- `issueDelete` is permanent and may require workspace permissions.
- List responses report `comment_count: 0` because Linear does not expose a
  cheap comment count. `tasks task show` returns the real count.
