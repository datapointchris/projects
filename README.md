# projects

A terminal UI for organizing personal projects. It holds the projects, the items that make each one
up, the dependencies between items, and the sub-tasks on each item. It reads and writes a local
SQLite database, so it works with no network at all. Sync to an
[ichrisbirch](https://github.com/datapointchris/ichrisbirch) API server is optional, and runs in the
background when it is on.

`projects` is the TUI and nothing more. Its subcommands are `sync`, `update`, `version` and
`completion`, and none of them reads or writes an item. Scripts and agents reach the same projects
and items through ichrisbirch's own CLI, against the same API.

## Install

```bash
go install github.com/datapointchris/projects@latest   # the main package is the module root
projects update                                         # replace the binary with the latest release
```

Release archives for Linux and macOS, on amd64 and arm64, are on the
[releases page](https://github.com/datapointchris/projects/releases). `projects update` replaces the
running binary with one of them, verified against the release's published checksums. It needs no Go
toolchain.

To build from source:

```bash
git clone https://github.com/datapointchris/projects
cd projects
go build -o projects .
```

A release build run in a terminal checks GitHub for a newer release at most once a day. It prints a
line naming one and never installs it. `PROJECTS_NO_AUTO_UPDATE`, set to any value, switches the check
off.

## Starting from scratch

```bash
go install github.com/datapointchris/projects@latest  # the binary, and nothing else
projects version                                      # proves the install, and opens no database
projects                                              # creates ~/.local/share/projects/projects.db, opens the TUI

mkdir -p ~/.config/projects                           # projects reads its config and never writes one
$EDITOR ~/.config/projects/config.toml                # [sync] enabled, api_url, token_command
projects sync                                         # binds the database to that API, then pulls
projects                                              # the status bar reads SYNCED and the API's URL
```

The first three lines are the whole setup for local use. The right end of the status bar reads
`LOCAL` and the database path, which shows `projects` is running without sync. In the TUI, `a` adds a
project, `Tab` moves to the item pane, and `a` adds an item there. `q` quits.

The second group is only for sync, and it comes before you add anything. Nothing written with sync
off is queued for push. The first pull deletes it as rows the server never returned. The guard under
[A database belongs to one API](#a-database-belongs-to-one-api-and-a-pull-that-would-empty-it-stops)
stops that only once a pull would delete ten projects or ten items. To keep local work and sync as
well, point `PROJECTS_DB` or `db_path` at a second database for the synced one.

`config.toml` takes the keys under
[Configuration](#configuration--one-toml-file-every-key-overridable-from-the-environment). A key
`projects` does not read is an error rather than ignored, so a misspelled key never passes for an unset
one.

## Coming from todoui — the first run moves the data, and the rest is by hand

This tool was called `todoui`. Quit any running copy, delete the binary (`rm ~/go/bin/todoui`), then
install this one. `todoui update` cannot cross the rename, because the release archive no longer holds
a binary of that name. Deleting it matters: one run of an old `todoui` after the move creates a new
empty database at the old path, and `projects` then stops until that file is gone.

Move `~/.config/todoui/config.toml` to `~/.config/projects/config.toml` first. While only the old file
exists, every run that opens a database stops with exit 1, naming both paths. Reading no config would
turn sync off, and the next pull deletes what was written with it off.

The first run that opens the default database moves everything in `$XDG_DATA_HOME/todoui` to
`$XDG_DATA_HOME/projects`. `todoui.db` becomes `projects.db`, and its `-wal` and `-shm` files go with
it, because the WAL can hold committed rows. `todoui.db` beside an existing `projects.db` stops the run
with exit 1, naming both paths, and nothing moves. Any other file already at its destination stays
behind under `todoui`.

A run where `PROJECTS_DB` or `db_path` names another database moves nothing, so a machine that always
names one moves any `repos.json` by hand. A database path inside `$XDG_DATA_HOME/todoui` stops the run,
because the first default run moves that directory out from under it.

A retired `TODOUI_*` variable set to a value stops every command with exit 2, naming the variable that
replaced it. Ignoring `TODOUI_DB` would open the default database where the variable chose another.

## The TUI — projects on the left, their items on the right, every key one press away

```text
j / k     move through items and their tasks as one list    J / K   jump item to item
Tab       switch pane                                       Enter   read it in full
a         add a project, or an item to this project         space   mark done
T         dependency browser                                /       search every project
u         undo the last change                              R       sync now
?         every key the pane you are in takes               q       quit
```

The status bar carries the keys the current row takes, and `?` lists every key for the pane.

The project pane opens on an `All` row, which shows every item grouped by project. `space` on a
project adds it to a selection. The item pane then shows the selected projects' items, grouped the
same way. An item belongs to one project or to several. `A` adds an item to more than one, and `p`
changes which projects hold it. Each item carries a title, notes, a checklist of sub-tasks, and the
items it waits on. The reader also names the repo the item is for. That tag is set through
ichrisbirch and arrives with sync. `projects` has no key that sets it.

`Enter` opens the reader, a full screen holding one item or one project. An item shows its fields,
what blocks it, what it blocks, its tasks, and its notes rendered as markdown. A project shows its
status, its open and done counts, and its description rendered the same way. `j`/`k` scroll a line,
`Ctrl+d`/`Ctrl+u` half a screen, and `g`/`G` jump to either end. `Esc`, `q` or `h` goes back to
where the reader was opened: the list on the same row, or the dependency browser. The item keys act
on the item shown, so `n` edits the note in `$EDITOR` and the reader shows the result. `u` undoes
the last change, and the reader shows that too. A placeholder written as `<word>` shows as written
rather than being read as an HTML tag. The list draws no note text. A `≡` on the row says the item
has one.

`b` links an item this one is blocked by. A link that would close a cycle is refused. `T` opens the
dependency browser. The left pane is every tree, searchable with `/`. The right pane is the tree you
are standing in. `l` drills in, so the row under the cursor becomes the root. `h` backs out one
drill at a time. `i` flips the tree between what an item waits on and what it unblocks. Finished
trees stay hidden until `a` shows them.

`x` archives an item. `1` filters to blocked items, `2` adds archived ones back, and `0` clears the
filter.

## A project is bounded work, and its name is never a repo's

A project is something that ends. `projects sync improvements` ends, and `projects` does not. A
project named after a repo becomes the bucket every later papercut falls into, because the repo
keeps existing after the work is done. An item's repo tag carries that association instead. The tag
crosses project boundaries and outlives any one project.

So creating a project refuses a name the repo registry holds, and so does renaming one. The registry
is a JSON file with a `repos` array, and each entry's `name` is a banned project name:

```json
{ "repos": [{ "name": "projects" }] }
```

The match ignores case and surrounding spaces. `projects` reads the first registry it finds, in this
order:

1. `$PROJECTS_REPOS_REGISTRY`
2. `repos_registry` in the config file
3. `$XDG_DATA_HOME/projects/repos.json`, which defaults to `~/.local/share/projects/repos.json`

A missing registry bans nothing. A registry that does not parse fails every create and rename with
the parse error.

A project's status is `active`, `someday`, `completed` or `dropped`. Reading a project, `c`
completes it, `x` drops it, `S` shelves it for someday, and `r` reopens a shelved or closed one.
Completing a project is what takes it out of the list, so there is no separate archive step the way
items have one. A shelved project leaves the list too, but records no closing date. `C` shows
shelved and closed projects again, each marked with its status.
Dropping requires a reason, because "deferred" invites the same idea back next month and "dropped,
and here is why" does not.

Closing a project does not touch its items, so an item left open shows the project closed with
work unfinished.

Only an active project holds its name. A completed `projects sync improvements` frees the name for the
next round of them.

## An item's handle is its number, and its key is a UUID

An item shows its number, the same handle ichrisbirch prints. The number is not the primary key.
`projects` creates every id as a UUIDv7, so an item can be created offline and pushed without a round
trip to claim one.

With sync on, the number comes from the API's response to the create, and the TUI shows it once the
push lands. With sync off, `projects` allocates the next number itself. An item created while the API is
unreachable shows the last 8 characters of its id until its push lands and earns it a number.

## Sync is automatic, and `projects sync` is for now rather than for correctness

With sync on, every edit is queued and pushed as you make it. The TUI pulls on launch, then again
every `sync.interval` (default two minutes) for as long as it is open. A pull waits while a text
input, an overlay, the reader or a move is open, and runs on the next tick instead.

Queued pushes retry on a timer. A change made while the API was unreachable is sent once it comes
back, with no further edit to trigger it.

The status bar names the API's URL beside one of `SYNCING...`, `PENDING (n)`, `SYNC ERR` or
`SYNCED`. An unreachable API shows `SYNC ERR` and leaves the local data in place. `R` in the TUI or
`projects sync` forces a reconcile.

A pull is two requests, because the item list embeds memberships, dependencies and sub-tasks.
Against an API that does not embed them, it falls back to two more requests per item. Lower
`interval` to sync more eagerly. It is floored at 15 seconds.

## A database belongs to one API, and a pull that would empty it stops

```bash
projects sync            # push anything queued, then pull, however recently the last pull ran
projects sync --adopt    # bind this database to the configured API, replacing the old binding
projects sync --force    # allow a pull that would delete most of the projects or items here
```

A pull deletes every local row the server did not return, so a database and its API are a pair.
The first pull records which API the database belongs to. Every pull and push after that refuses any
other. A database pointed at the wrong API would lose everything that API has never heard of, and
the reconcile cannot tell that from a real deletion.

A database that has pulled before but records no API refuses as well. It asks for `--adopt` rather
than guessing from whatever is configured, because guessing from the loaded configuration is the
mistake the binding exists to stop.

The second guard is for the right API giving a wrong answer. A pull that would delete ten or more
projects, and more than half of them, stops and asks for `--force`. Items are guarded the same way.
A truncated response, an authentication failure that still returns `200`, and a genuine mass
deletion look identical from the client. The floor keeps the guard quiet on a small database, where
losing most of it is a handful of rows.

Neither flag is reachable from the background timers.

## Configuration — one TOML file, every key overridable from the environment

`projects` reads `$XDG_CONFIG_HOME/projects/config.toml`, else `~/.config/projects/config.toml`, on macOS
as well as Linux. No config file is a valid configuration: a local database, with sync off.
[config.example.toml](config.example.toml) is an annotated copy.

```toml
# repos_registry = "/path/to/repos.json"  # a bare key, so it sits above the first [section]

[local]
# db_path = "/custom/path/projects.db"  # default: ~/.local/share/projects/projects.db

[sync]
enabled = false
# api_url = "https://example.com"  # the server's host; the tool adds /api/v1
# token_command = "<cli> auth token"  # prints a bearer token for api_url
# interval = "2m"  # longest it goes without reconciling; floored at 15s
```

`enabled = true` without an `api_url` is an error.

`token_command` is how `projects` authenticates to the API without holding a secret itself. It is split
on spaces and run without a shell or stdin. Whatever it prints becomes the bearer token. A command
that has not finished in 10 seconds fails. `projects` keeps the token until the API refuses it, then
runs the command once more and retries once. A failing command's first line of stderr is what the
status bar shows. Leave it unset for an API that needs no token, such as a dev stack that sets its
own identity.

The command belongs to the URL beside it. When `PROJECTS_SYNC_URL` supplies the URL, the config
file's `token_command` is ignored and `PROJECTS_SYNC_TOKEN_COMMAND` is the only source. Overriding the
URL never sends one API's token to another.

Each variable overrides the key beside it:

| Variable | Overrides |
| -------- | --------- |
| `PROJECTS_DB` | `local.db_path` |
| `PROJECTS_SYNC` | `sync.enabled` (`true` or `false`) |
| `PROJECTS_SYNC_URL` | `sync.api_url` |
| `PROJECTS_SYNC_TOKEN_COMMAND` | `sync.token_command` |
| `PROJECTS_SYNC_INTERVAL` | `sync.interval`, as a duration such as `30s` or `5m` |
| `PROJECTS_REPOS_REGISTRY` | `repos_registry` |

## Local-first — SQLite is the store, and sync is a layer over it

Every read and write goes to the embedded SQLite database. The optional sync engine pushes mutations
and pulls remote state in the background.

```text
TUI
    |
    v
Backend interface
    |
    +-- service.Service (every rule, always)
    |       |
    |       +-- store.Store -> store/sqlite (SQLite)
    |
    +-- SyncBackend (wraps the service, optional)
            |
            +-- Push: pending_sync queue -> HTTP to ichrisbirch
            +-- Pull: HTTP from ichrisbirch -> upsert local SQLite
```

The data model is projects, items, the memberships joining them many to many, dependencies between
items, and sub-tasks.

`projects` writes two places. The database at `db_path` holds the data, the queue of pending pushes, and
the API the database is bound to. The update check keeps its last result under
`$XDG_STATE_HOME/projects/`, which defaults to `~/.local/state/projects/`. The config file and the repo
registry are read and never written.
