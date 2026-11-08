# projects

A command line and a terminal UI for organizing personal projects. It holds the projects, the items
that make each one up, the dependencies between items, and the sub-tasks on each item.

The config file chooses where the data lives. With no `api` key, every command reads and writes a
SQLite database on this machine, so it works with no network at all. With `api` set, every command
reads and writes that API and opens no database. `projects-api`, in this repo, is that API, on
Postgres. Both modes run the API's own routes: in database mode they are served inside the process,
so a refusal, an exit code and a `--json` answer are the same in both. `web/` is a browser page over
that server, served beside it.

```bash
projects                      # help: every command, grouped
projects next --limit 1       # the one item to work on next
projects items show 42        # one item with its tasks and blockers
projects tui                  # the board, where everything is a key press
```

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
projects create --name "Repaint the fence"            # creates ~/.local/share/projects/projects.db
projects tui                                          # the board, reading that database
```

Those lines are the whole setup for database mode. The right end of the board's status bar reads
`LOCAL` and the database path. In the board, `a` adds a project, `Tab` moves to the item pane, and `a`
adds an item there. `q` quits.

To work through an API instead:

```bash
projects auth login https://projects.example.com      # writes api, issuer and client_id to the config file
projects list                                         # now reads the API, and opens no database
projects tui                                          # the status bar reads API and the API's host
```

`auth login` fills in the config file, and the `api` key it writes is what switches every command to
the API. A database already on the machine is left as it is, and nothing copies it to the API.

`config.toml` takes the keys under
[Configuration](#configuration--one-toml-file-every-key-overridable-from-the-environment). A key
`projects` does not read is an error rather than ignored, so a misspelled key never passes for an unset
one.

## The project verbs sit at the root, and the item verbs under `projects items`

`projects --help` lists every command, grouped. `projects items --help` lists the item verbs, with
`tasks`, `dependencies` and `memberships` as nouns under them.

```bash
projects show "Kitchen remodel"                          # a project and its open items
projects items create --project "Kitchen remodel" --title "Get three quotes"
projects items dependencies add 43 --depends-on 42       # 43 waits on 42
```

- `<project>` is a project's name or UUID. `<item>` is an item's number or UUID.
- An answer goes to stdout. A hint, a prompt and an error go to stderr.
- Every read takes `--json`, which prints one JSON document on stdout. A list is a bare array, and an
  empty one is `[]`.
- A `--json` document this tool composes carries `schema_version`, and [cli/schemas](cli/schemas)
  holds its JSON Schema. Every other `--json` answer is the API's own shape, under
  [api/schemas](api/schemas).
- `next` prints a capped list, and `--limit` sets the cap. A list under `projects items` prints every
  row unless `--limit` caps it.
- In database mode, `--repo` takes a name from the repo registry. Through an API it takes any tag,
  because tags come from every machine sharing the API. On a filter, `--repo ""` means untagged work.
  On `items edit`, it clears the tag.
- `items create` in a terminal asks for the title and projects the flags left out. Typing `+` at the
  project question creates a project on the spot. `--no-input`, or no terminal, fails instead and
  names the flag that would have answered.
- `delete` and every `remove` ask first. `--yes` skips the question. With `--no-input`, or no
  terminal, they refuse unless `--yes` is passed.
- `projects tui --once` prints one frame at `COLUMNS` by `LINES` and exits. `projects tui --json`
  prints what the board is drawn from. With neither and no terminal, `projects tui` refuses.

| Exit | Meaning |
| ---- | ------- |
| `0` | Done |
| `1` | Refused or failed: not found, not logged in, a rule said no, or a write the API may have carried out |
| `2` | A usage error: an unknown flag, a missing argument, a value the API refused as input |
| `4` | Running it again is safe and may succeed: the API or the login provider gave no answer, a `429`, or a `5xx` to a read |

A read is retried before it exits 4, on a failed connection, an answer cut short, or a `429`, `502`,
`503` or `504`. A write is retried only where the connection never opened, so it is never sent twice.
A write that was sent and got no answer, or a `5xx`, exits 1 and says it may have been carried out,
because running it again could do it twice.

## Through an API, a login on this machine carries every command

`projects auth login <url>` reads the API's protected-resource metadata to find the identity provider,
then shows a code to approve in any browser, on any device. It writes `api`, `issuer` and `client_id`
into the config file and keeps the token in the OS keychain. Where no keychain answers, the token goes
to a file under `$XDG_STATE_HOME/projects/`. `auth status` names which one holds it.

The client id defaults to `projects-cli-<hostname>`. Each machine logs in as its own client, and the
API reads that prefix as a person.

`auth token` prints the access token, refreshed when it has expired. `auth logout` removes it.

A service has no person to approve a code. With `PROJECTS_CLIENT_SECRET` set, every command
authenticates as `client_id` with the client-credentials grant, asking for `projects.items.read`.
That client is a confidential client registered for the service alone, and its id starts
`projects-svc-`. `auth login` refuses while the secret is set.

`token_command` in the config file replaces both. It is an argument list, run without a shell. Its
stdout is a token response, a JSON object holding `access_token`, and no login runs. An API that needs no real token,
such as a dev stack whose proxy sets the caller's identity, takes a command that prints a stand-in.

`--api <url>` on any command outranks the file's `api` for one run. An address other than the file's
must name the file's issuer in its own metadata, so a token is never sent to an API that does not
trust the provider that issued it.

## Coming from todoui — the first run moves the data, and the rest is by hand

This tool was called `todoui`. Quit any running copy, delete the binary (`rm ~/go/bin/todoui`), then
install this one. `todoui update` cannot cross the rename, because the release archive no longer holds
a binary of that name. Deleting it matters: one run of an old `todoui` after the move creates a new
empty database at the old path, and `projects` then stops until that file is gone.

Move `~/.config/todoui/config.toml` to `~/.config/projects/config.toml` first. While only the old file
exists, every run that opens a database stops with exit 1, naming both paths. Reading no config would
drop its `api` and send every command to a database on this machine.

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
u         undo the last change                              R       read the data again now
?         every key the pane you are in takes               q       quit
```

The status bar carries the keys the current row takes, and `?` lists every key for the pane.

The project pane opens on an `All` row, which shows every item grouped by project. `space` on a
project adds it to a selection. The item pane then shows the selected projects' items, grouped the
same way. An item belongs to one project or to several. `A` adds an item to more than one, and `p`
changes which projects hold it. Each item carries a title, notes, a checklist of sub-tasks, and the
items it waits on. The reader also names the repo the item is for. The board has no key that sets
that tag. `projects items edit <item> --repo <name>` sets it.

Through an API, the status bar reads `API` and the API's host. The board reads the API again every
`apiRereadInterval` in `tui/app.go`, and `R` reads it at once.

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

So creating a project refuses a name the repo registry holds, and so does renaming one. That holds
in database mode only. Through an API it bans nothing, because the API holds no registry. The
registry is a JSON file with a `repos` array, and each entry's `name` is a banned project name:

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
completes it, `x` drops it, `S` shelves it for someday, and `r` reopens a shelved or closed one. On
the command line those are `projects complete`, `close --reason`, `shelve` and `reopen`.
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

An item shows its number, the same handle the API answers with. The number is not the primary key.
`projects` creates every id as a UUIDv7.

Through an API, the API numbers the item as it creates it. In database mode, `projects` allocates the
next number itself. An item with no number shows the last 8 characters of its id. Only a database
from before numbers existed, or one kept as a synced copy of an API, can hold such an item.

## A synced copy's config and database still load, while a sync variable or an unpushed edit refuses

A config holding `[sync]`, a `PROJECTS_SYNC*` variable, or a database that was a synced copy of an
API behaves as follows. `api` is how a machine works through an API.

- A config file still holding `[sync]` loads. Every command that opens data warns on stderr, naming
  the file, and ignores the section.
- A database that was a synced copy opens as it is. The first open drops its queue of pushes, its pull
  times and the API it was bound to. Every project, item, task, dependency and membership stays.
- A queue still holding an edit stops that open with exit 1, naming how many and the API they were
  for, because those edits never reached it. `projects sync` from v1.24.2 pushes them, and
  `DELETE FROM pending_sync` discards them.
- `PROJECTS_SYNC`, `PROJECTS_SYNC_URL`, `PROJECTS_SYNC_TOKEN_COMMAND` or `PROJECTS_SYNC_INTERVAL`, or
  its `TODOUI_` twin, set to a value stops every command with exit 2. `PROJECTS_SYNC_URL` chose an
  API, so ignoring it would send the run wherever the config file points.

## Configuration — one TOML file, every key overridable from the environment

`projects` reads the file `$PROJECTS_CONFIG` names, else `$XDG_CONFIG_HOME/projects/config.toml`, else
`~/.config/projects/config.toml`, on macOS as well as Linux. Every command reads that one file, and
`auth login` writes it. No config file is a valid configuration: a local database.
[config.example.toml](config.example.toml) is an annotated copy.

A `PROJECTS_CONFIG` naming a file that does not exist stops every command with exit 2, except
`auth login`, which creates the file there. Reading no config would open the default database, so a
write would land where nobody is looking.

```toml
# Bare keys sit above the first [section].
# repos_registry = "/path/to/repos.json"

# api = "https://projects.example.com"  # set, every command goes to this API and no database opens
# issuer = "https://auth.example.com/application/o/projects/"  # auth login writes it from the API
# client_id = "projects-cli-desk"  # auth login writes it; default projects-cli-<hostname>
# token_command = ["printf", '{"access_token":"dev","token_type":"Bearer"}']  # replaces the login

[local]
# db_path = "/custom/path/projects.db"  # default: ~/.local/share/projects/projects.db
```

Each variable overrides the key beside it:

| Variable | Overrides |
| -------- | --------- |
| `PROJECTS_CONFIG` | the whole file |
| `PROJECTS_DB` | `local.db_path` |
| `PROJECTS_REPOS_REGISTRY` | `repos_registry` |

`api`, `issuer`, `client_id` and `token_command` take no variable of their own, because they name one
environment together. `PROJECTS_CONFIG` swaps all four at once. `--api` outranks `api` for one run.
`PROJECTS_CLIENT_SECRET` switches the login to the client-credentials grant.

A checkout develops against a database of its own through an `.envrc`, which
[direnv](https://direnv.net) loads on entering it:

```bash
export PROJECTS_CONFIG="$PWD/config.dev.toml"
export PROJECTS_DB="$HOME/.local/share/projects/dev.db"
```

[config.dev.toml](config.dev.toml) holds no `api`, so a command run there opens `dev.db` even where the
machine's own config sends every command to an API. The two lines are one environment. `PROJECTS_DB`
alone leaves the machine's `api` in force, and the run goes to that API. Where no direnv hook runs, such
as a script, `direnv exec . projects list` loads the file for one command.

## Every command speaks HTTP to the API's routes, served in the process or across the network

A command reads and writes through one HTTP client. In database mode its transport hands each
request to the API's own routes inside the process, over the SQLite store. Through an API, the
transport is the network. So a rule, a refusal and a `--json` shape are one thing in both modes.

```text
projects <command>                     projects tui
    |                                      |
    v                                      v
apiclient.Client                       backend.Backend
    |                                      |
    +-- database mode                      +-- database mode: service.Service
    |     InProcess -> api/routes          |
    |       -> service.Service             |
    |       -> store/sqlite                +-- api: apiclient.Backend
    |                                            -> apiclient.Client
    +-- api: HTTP, retried
          -> projects-api
```

The data model is projects, items, the memberships joining them many to many, dependencies between
items, and sub-tasks.

In database mode, the database at `db_path` holds the data and the undo log. The update check keeps its last result under `$XDG_STATE_HOME/projects/`,
which defaults to `~/.local/state/projects/`. `auth login` is the only command that writes the config
file, and it adds its keys without rewriting the rest. The repo registry is read and never written.

## The API — the same rules over HTTP, on Postgres

`projects-api` serves projects, items, dependencies and tasks under `/api/v1`. It runs the routes and
rules the command line runs in database mode, over a Postgres store instead of SQLite, so a rule
behaves the same in both. The one exception is
the ban on a project named for a repo, which reads a registry file this server does not hold.

```text
projects-api
    |
    v
api/routes      decode, call, encode
    |
    v
service.Service every rule, shared with database mode
    |
    v
store/postgres  pgx, goose migrations, a row trigger writing audit_trail
```

Every setting comes from the environment:

| Variable | Default | |
| -------- | ------- | |
| `OIDC_ISSUER` | required | The provider whose RFC 9068 access tokens the API accepts |
| `PUBLIC_URL` | unset | The address clients reach the API at. Set, it serves RFC 9728 metadata naming the issuer. Required with `WEB_CLIENT_SECRET` |
| `WEB_CLIENT_ID` | `projects-web` | The confidential client the API logs a browser in as |
| `WEB_CLIENT_SECRET` | unset | That client's secret. Set, the API logs browsers in. Unset, only a bearer token reaches it |
| `POSTGRES_HOST` | required | |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_USERNAME` | required | |
| `POSTGRES_PASSWORD` | required | |
| `POSTGRES_DB` | required | |
| `PORT` | `8000` | Where it listens |

`projects-api migrate` brings the schema up to date, adds any new kind or status, and exits. It reads
only the `POSTGRES_` settings. With no argument, `projects-api` serves, and it never migrates on its own.
Two pods starting together would race, and a bad migration would crash-loop what is serving.

```bash
docker run --rm --env-file settings.env ghcr.io/datapointchris/projects:latest projects-api migrate
docker run --rm --env-file settings.env -p 8000:8000 ghcr.io/datapointchris/projects:latest
```

`/health` answers for the process. `/ready` answers 503 while Postgres or the identity provider does
not. A request that outlasts `requestDeadline` in `api/routes/routes.go`, as when Postgres stops
answering, answers 503. TERM stops new requests and lets the ones in flight finish. It exits zero if
they finish inside `drainTimeout` in `api/main.go`, and non-zero if one outlasts it. Logs are one JSON
object per line on stdout, and each request's line names its caller, its `X-Request-ID` and its
`traceparent`.

**A client's id says who it is.** A token from a client whose id starts `projects-cli-` acts as the person
who logged in, and reaches every route. One starting `projects-svc-` is a service, and reaches only what
its scopes list in `routes.ServiceScopes`. `projects.items.read` is the read scope. A client with neither
prefix answers 401, whatever it holds, so moving a caller onto this API takes a client of its own. A
browser's session acts as its person, as a `projects-cli-` token does.

**`{id}` takes more than an id.** A project answers to its UUID or its name, and an item to its UUID or
its number. A name holding a slash is reached by escaping it, as `/api/v1/projects/web%2Fapi`.

**A row's every field is always sent: an empty value as `null`, and an empty list as `[]`.** In a `PATCH`, a field left out stays as it is,
and a field sent as `null` clears it. A field that cannot be empty refuses `null` by name.

A refusal is `{"error": <status phrase>, "message": <why>}`. A refused input adds `errors`, listing each
field with its `loc` and `msg`. [api/schemas](api/schemas) holds a JSON Schema for every answer, and
`api/wire` holds the Go structs.

**Each release publishes both images and their manifests under one version.**
`ghcr.io/datapointchris/projects` is the API's image, `ghcr.io/datapointchris/projects-web` is the web
app's, and `ghcr.io/datapointchris/projects/manifests` is an OCI artifact holding `migrate/` and `app/`.
Apply `migrate/` and wait for its Job before applying `app/`. All three are signed keylessly through
Sigstore, and the release's notes name each by digest:

```bash
cosign verify --certificate-identity https://github.com/datapointchris/projects/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com ghcr.io/datapointchris/projects-web:latest
```

`deploy/app/api.yaml` and `deploy/app/web.yaml` list what a cluster supplies around those manifests.

## The web app — one page, on the API's own origin

`web/` is a Vue page at `/`: the projects on the left, the open project's items on the right, and a
search over every item. It holds no rule. Each change is a call to the API, and the page shows what the
API answers.

The `projects-web` image is Caddy on port 8080, serving the built page and passing `/api` and
`/.well-known` to `projects-api:8000`. A host's route sends every path to it, so the browser sees one
origin and needs no CORS. A path escaped as `%2F` reaches the API escaped.

```text
browser ──→ projects-web:8080 ── /api, /.well-known ──→ projects-api:8000
                 │
                 └── every other path: the page
```

**The API logs the browser in, and the browser never holds a token.** `/api/auth/login` sends it to the
provider as the `WEB_CLIENT_ID` client, and `/api/auth/callback` sets an httpOnly, SameSite=Strict
session cookie. The session lives in Postgres, in `browser_sessions`. Every request the page sends
carries `X-CSRF: 1`, and the API refuses a session's request without it. The provider's client needs
`PUBLIC_URL` + `/api/auth/callback` as its redirect URI, the `authorization_code` grant with S256 PKCE,
`client_secret_basic` at the token endpoint, and the `openid` and `profile` scopes. A provider that
advertises pushed authorization requests receives the login that way.

When a session ends, a change fails where it was made, with a message, rather than leaving the page and
losing it. So does a search, whose query exists nowhere else. Any other read goes to the login and
comes back, at most twice a minute.

The page's Log out button posts `/api/auth/logout`, which ends the session at the API. The provider
keeps its own, so logging in again may complete without asking for anything. Ending that one is the
provider's logout page.

While the API cannot yet reach the provider or the session tables, it serves every bearer request. A
login answers a 503 page, and a request carrying a session answers 503. A `PUBLIC_URL` that is not
https, or http on a loopback host, stops the API at start, because no browser would keep its cookie.

`docker-compose.dev.yml` runs the whole stack from this checkout at <http://localhost:8096>, with a
stand-in provider that logs every login in as one person:

```bash
task dev-stack:start   # Postgres, the migration, the API, the web image and the provider
task web:dev           # the page from source at http://localhost:3002, reloading on each edit
task web:test:e2e      # a fresh stack, one Playwright roundtrip through it, then the stack removed
task dev-stack:stop    # stops it and deletes its database
```
