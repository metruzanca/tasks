---
name: tasks
description: Use the local tasks kanban board as the source of truth for project work. Use when the user asks what to work on next, asks to add or update tasks, wants a work plan tracked, or wants the board cleaned up.
---

# Tasks board

`tasks` is a terminal kanban board shared by the developer and agents. It is not
agent memory: do not store scratch notes, research, or conversation summaries in
it unless the user asks for a task. Add real, actionable work only.

## Workflow

1. Find the current work:
   - `tasks next --json` returns the highest-priority ready task or `null`.
   - `tasks list --status todo --json` lists ready work.
   - `tasks list --json --status in_progress` shows what is already claimed.
2. Claim work before starting:
   - `tasks move <id> in_progress --assignee @me`
   - `tasks comment <id> "Starting: <short plan>"`
3. Record new work discovered during the task as a separate, specific task:
   - `tasks add "Fix expired session redirect" -d "Repro: ..." -p high -l bug`
   - Search first (`tasks list --search "..." --all --json`) to avoid duplicates.
4. Record findings that matter to the next person:
   - `tasks comment <id> "Cause: ..."`
5. Finish only when it is true:
   - `tasks done <id>`
   - Move to review instead when the user needs to verify:
     `tasks move <id> in_review`

## Rules

- Read the board before creating tasks. Do not duplicate an existing task.
- Prefer one concrete deliverable per task. Avoid vague epics.
- Never delete or hard-delete tasks. Archive only when the user asks.
- Do not move tasks to done without evidence that the work is complete.
- Use `--json` when you need to parse output. Errors go to stderr and exit non-zero.
- Tasks are shared with humans: write titles and comments they can understand.
- If `tasks` is not configured, run `tasks doctor` and report the setup problem
  instead of inventing a task store.

## Commands

```
tasks next --json
tasks list --json [--status todo] [--label bug] [--priority high] [--search text]
tasks show <id> --json
tasks add "title" -d "details" -p high -l bug
tasks move <id> in_progress|in_review|done
tasks edit <id> --title "..." --description "..." --priority high
tasks comment <id> "text"
tasks comments <id> --json
tasks columns --json
```

IDs look like `T-12` for SQLite and `42` / `#42` for GitHub. Use the ID printed
by `add` or returned in JSON.
