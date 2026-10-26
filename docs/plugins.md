# External plugins

SQLite, GitHub Projects, and Linear are native backends compiled into the
`tasks` binary. Any other backend is an external plugin: a separate
`tasks-plugin-<name>` executable that speaks the same gRPC protocol.

Native names cannot be installed or removed. For everything else, use:

```sh
tasks plugin list
tasks plugin install <module>@<version> --name <name>
tasks plugin remove <name>
tasks plugin path <name>
```

`plugin install` runs `go install` with `GOBIN` set to the plugin directory, then
starts the plugin and calls `Health` to verify it. The module must be published
for `go install` to work.

## Discovery

Plugins are discovered as `tasks-plugin-<name>` in this order:

1. `plugin_dir` from config
2. `~/.local/share/tasks/plugins`
3. next to the `tasks` executable
4. `PATH`

For local development, build the plugin into the plugin directory:

```sh
go build -o "${XDG_DATA_HOME:-$HOME/.local/share}/tasks/plugins/tasks-plugin-<name>" \
  ./cmd/tasks-plugin-<name>
tasks doctor
```

## Writing a backend

1. Implement `sdk.Backend` (`pkg/sdk/backend.go`).
2. Read your section from the JSON in `TASKS_PLUGIN_CONFIG`.
3. Call `sdk.Serve(backend)` from `main`.
4. Name the binary `tasks-plugin-<name>`.

`Describe` advertises columns, capabilities, and the ready column. The host
renders tables, JSON, and the TUI, so a plugin only implements persistence
semantics. The protocol is defined in `proto/backend.proto`.

If your backend supports `tasks auth login`, implement `sdk.Authenticator` as well.
The host exposes it through the optional auth service, and plugins can store
tokens with the helpers in `pkg/sdk/credentials.go`. Credentials live in the
file named by `TASKS_CREDENTIALS_FILE`, not in the config file.
