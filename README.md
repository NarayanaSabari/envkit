# envkit

`envkit` keeps each project's `.env` outside its worktrees in `~/.envkit`.
That directory is a local Git repository, so changes have a small audit log.
This helps keep secrets out of agent transcripts, repository diffs, and accidental commits.

## Install

Install the current release with Go:

```sh
go install github.com/NarayanaSabari/envkit@latest
```

Or build and install from a checkout:

```sh
make install
```

`make install` puts the single Go binary in `~/.local/bin/envkit`.

## Quick start

```sh
cd my-project
printf '%s' 'example-secret' | envkit set API_TOKEN -m "Development API token"
envkit ls
# API_TOKEN  # Development API token
envkit run -- ./script-that-needs-the-token
```

`envkit` stores the file at `${ENVKIT_HOME:-$HOME/.envkit}/<project>/.env`.
A project name is resolved from `ENVKIT_PROJECT`, `git config envkit.project`, the origin owner and repository, then the worktree directory.
All worktrees with the same origin therefore share one env file.
`init` creates the home directory with mode 700 and initializes its Git repository.

Values are written as-is, with no quoting or shell expansion.
Pipe values with spaces or shell syntax directly to `envkit set`.

## Verbs

| Verb | Description |
| --- | --- |
| `init` | Initialize storage and print the project env path. |
| `path` | Create if needed, then print the `.env` path. |
| `project` | Print the resolved project name. |
| `set KEY [-m COMMENT]` | Read a secret from stdin or a hidden terminal prompt and save it. |
| `unset KEY` | Remove a key and its directly preceding comments. |
| `comment KEY TEXT` | Set the comment directly above an existing key. |
| `ls` | List keys and comments, never values. |
| `get KEY` | Print one value. |
| `run [--] CMD...` | Execute a command with the project env loaded. |
| `export` | Print shell `export` statements. |
| `log [KEY]` | Show project history, or pickaxe history for one key. |
| `diff` | Show uncommitted changes in the env storage repository. |
| `edit` | Edit with `$EDITOR`, then commit changes. |
| `list` | List all saved projects. |
| `tui` | Open the interactive terminal interface. |

For agents and scripts, prefer a command boundary instead of printing secrets:

```sh
envkit run -- your-command
```

When a process must load the file itself:

```sh
set -a; . "$(envkit path)"; set +a
```

## TUI

`envkit tui` is a built-in Bubble Tea interface inspired by lazydocker.
It has a project pane, masked key table, selected-key comment and history pane, and a status bar.
Values are revealed only for the selected row and re-mask when the cursor moves.
Every mutation uses the same local Git audit log as the CLI.

| Key | Action |
| --- | --- |
| `tab`, `shift-tab` | Switch between Projects and Keys panes. |
| `j` / `k`, arrows | Move the cursor in the focused pane. |
| `enter` | Load the selected project. |
| `a` | Add a key, hidden value, and optional comment. |
| `e` | Edit the selected value with a hidden input. |
| `c` | Edit the selected comment. |
| `d`, then `y` / `n` | Delete the selected key with confirmation. |
| `v` | Reveal or mask the selected value. |
| `/` | Filter the focused pane. |
| `r` | Refresh projects and keys. |
| `esc` | Cancel an inline input. |
| `q`, `ctrl-c` | Quit. |

## Secret output warning

`ls`, logs, and normal mutation commands do not print values.
`get` and `export` intentionally print secrets, so use them only in a safe terminal context.
