# envkit

`envkit` keeps each project's `.env` outside its worktrees in `~/.envkit`.
That directory is a local Git repository, so changes have a small audit log.
This helps keep secrets out of agent transcripts, repository diffs, and accidental commits.

## Install

```sh
chmod +x envkit
ln -s "$PWD/envkit" ~/.local/bin/envkit
```

Or download the script into a directory on your `PATH`, such as `~/.local/bin`.

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

Values are written as-is, with no quoting added.
If a value contains spaces or shell syntax, provide shell quotes yourself, for example `"two words"`.

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
| `tui` | Open the optional Gum-based interactive interface. |

For agents and scripts, prefer a command boundary instead of printing secrets:

```sh
envkit run -- your-command
```

When a process must load the file itself:

```sh
set -a; . "$(envkit path)"; set +a
```

## TUI

`envkit tui` requires [Gum](https://github.com/charmbracelet/gum).
It lets you filter projects and set values with a password field, edit comments, remove keys with confirmation, view history, run commands, and switch projects.

## Secret output warning

`ls`, logs, and normal mutation commands do not print values.
`get` and `export` intentionally print secrets, so use them only in a safe terminal context.
