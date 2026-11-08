# projects — Claude Code instructions

The main package is at the module root — `go build .`, `go install github.com/datapointchris/projects@latest`.
`api/main.go` is the HTTP API, `projects-api`, which `go install` of the root does not build.

## How to run and test

**A hand run is `direnv exec . go run .` from the repo root, against the dev
database. Do not build a throwaway binary to `/tmp` for one, and do not seed a
synthetic database for one.** A test fixture is allowed, and it needs at least one
long note.

`.envrc` pairs a dedicated dev database with the sync API's local dev stack, so
nothing run here touches production data. This is the whole file:

```bash
export PROJECTS_DB="$HOME/.local/share/projects/dev.db"
export PROJECTS_SYNC=true
export PROJECTS_SYNC_URL="https://api.docker.localhost"
```

The dev API needs no token, because its proxy injects the user header. The
server's own repo starts it, and the first sync binds `dev.db` to it. Its seed
data carries notes one line long. A change about notes needs a real-length
markdown note written into the dev API first.

⚠️ **`.envrc` is machine-local and is not in this repo. Check it exists before
running anything here.** Without it nothing is overridden, so `go run . tui` opens
the production database against the production API — which is the accident the
coupled-variables rule below exists to prevent, arrived at from the other
direction. `task setup`, which `worktree new` runs, links the primary
checkout's `.envrc` into a worktree.

**A shell without direnv's hook ignores `.envrc`, and Claude Code's tool shell is
one.** There, a bare `go run . list` reads production even with the file in place.
Load it explicitly:

```bash
direnv exec . go run . tui    # the board against dev.db
direnv exec . go run . list   # a command against dev.db
direnv exec . go run . sync   # one reconcile against the dev API
direnv exec . env | rg '^PROJECTS'   # nothing printed: no .envrc here
```

`.envrc` turns sync on, so a command that writes refuses there, naming `api`. Reads
answer from `dev.db`. A write is checked in the board, or through `both_modes_test.go`.
A config file that sets `api` beside the `PROJECTS_SYNC=true` from `.envrc` refuses
every command, naming both, so the pair cannot cross into production that way either.

**The former name is handled in one file.** `config/former_name.go` refuses a retired variable that is
still set, and a config still at the former path. It moves the old data directory on the first run
that opens the default database. A run naming its database moves nothing, so a run from `.envrc` leaves
the real data where it is. An `.envrc` still exporting the old names is refused rather than ignored,
which keeps it from falling through to the production database. On a machine upgrading, run `projects`
once on the default database before editing `.envrc`, so `dev.db` moves with the rest. The `MIGRATION`
marker there says when the file retires. Anything that opens the database stays after
`MoveDataUnderFormerName` in `openDatabase` in `cli/data_source.go`, because opening first creates an
empty database, and the move refuses to run over one.

Build, vet and unit tests check code correctness, not TUI correctness. A TUI change is verified by
driving the built binary, which the flows below do with nobody at the keyboard.

`tui/app_test.go` drives the model directly: `newTestApp` builds an app over an in-memory DB, and a
test feeds it `tea.KeyPressMsg` values and asserts on state or on `screen()`. `send` follows the whole
command chain, so a create and its refresh have both landed before the assertion, and it abandons
`tea.Tick` commands rather than sleeping out their duration. A layout bug that appears only at one
window size is reproducible here and invisible in a manual pass. It does not replace driving the TUI.

## The flows drive the real binary

`real_binary_flows_test.go` builds `projects` and replays each `testdata/flows/*.keys` script against
`projects tui` on a pty with [tuidrive](https://github.com/datapointchris/tuidrive), at a fixed size.
Each frame a script names compares with `testdata/TestRealBinaryFlows/<flow>/<frame>.txt` and `.style`.

```bash
go test -run TestRealBinaryFlows .                     # compare
TUIDRIVE_UPDATE=1 go test -run TestRealBinaryFlows .   # accept a change, then read the diff
go test -run TestTheCommandsAnswerTheSameInBothModes . # the commands, in both modes
```

- **`seedFixtureDatabase` writes through `service.Service`**, the path the TUI writes through, so
  the fixture holds only what the tool's own rules allow. There is no stand-in of the sync API, and sync
  stays off because no `PROJECTS_SYNC` reaches the program.
- **`XDG_CONFIG_HOME` points under the test's temporary directory**, so the machine's own config file
  never reaches the program.
- **The API is `routes.New` over a seeded SQLite database in an `httptest.Server`.** `serveOverHTTP`
  starts it and `writeAPIConfig` points the config file's `api` at it, with a `token_command` handing
  a stand-in token. The `api-mode` flow drives the board through it, and `both_modes_test.go` runs the
  same commands through it and through a database, checking each `--json` answer against
  `cli/schemas`.
- **`PROJECTS_DB` is relative, and the program's working directory is the test's temporary directory.**
  The status bar prints the path, and the hints clip against its length. A relative path keeps every
  frame the same on every machine.
- **A wait after a create names the row, not the hints.** A status flash holds the hints back for
  `statusFlashDuration`, and it lands before the refresh that draws the row.
- **A script masks each value that changes between runs.** A date left in a golden fails the next day.
- **Search results sort by a creation time the fixture cannot hold still**, so the search flow's query
  matches one item.

To look at something while developing, write the keys to a script and replay it:

```bash
D=$(mktemp -d)
go build -o "$D/projects" . && tuidrive run look.keys --out frames --dir "$D" \
  --env PROJECTS_DB=projects.db --env XDG_CONFIG_HOME="$D/config" -- "$D/projects" tui
```

`tuidrive run --help` states what reaches the program and what `--out` writes. Always name `PROJECTS_DB`
and `XDG_CONFIG_HOME` under a temporary directory, whatever it says. With neither, `projects tui` opens
the production database, or reads the machine's config and talks to its API.

## Storage, service and entry point are three layers, and every rule is in `service`

Ports and adapters at the storage seam, so a second store runs the same rules and a new rule lands once.

```text
cli commands                tui                          api/main.go
    │ apiclient.Client          │ backend.Backend, which        │
    │                           │ sync.SyncBackend and          │
    ▼                           │ apiclient.Backend also        ▼
api/routes, in process          │ satisfy                    api/routes
    │ in database mode;         │                               │ *service.Service, scoped
    │ across HTTP to            │                               │ to each request by
    │ projects-api otherwise    │                               │ WithContext
    ▼                           ▼                               ▼
service.Service      every rule
    │ store.Store    model types only
    ▼
store/sqlite         Open, migrations, queries.sql, generated
store/postgres       the same, with goose migrations and an audit trigger
```

- **`store/sqlite` opens the database and holds no decision.** It owns the schema, the migrations,
  `queries.sql` and sqlc's `generated`. It turns the driver's no-row answer, and each unique answer
  `store/store.go` promises a sentinel for, into `model` sentinels, so `service` matches no driver text
  and imports no `database/sql`.
- **`service.Service` satisfies `backend.Backend`**, so the TUI reaches every rule through that
  interface, and outside its tests imports neither `service` nor a store.
- **A command reaches the data only through `apiclient.Client`.** In database mode `openDatabase` in
  `cli/data_source.go` serves `routes.New` over the local service through `apiclient.InProcess`. So a
  command meets the API's routes, refusals and JSON in both modes, and a new command is written once.
- **The binary links no server dependency.** `go list -deps .` holds no pgx, goose or goapiauth beyond
  `goapiauth/clientid`, the import-free client-id prefixes goclilogin shares with the API. `api/routes`
  and `api/wire` import none of them. The login settings sit in `cli/api_login.go` rather than
  `config`, because `api/routes` reaches `config` through `service` and `repos`.
- **`store.Store` in `store/store.go` takes and returns `model` types.** A rule that writes runs inside
  `InTx(ctx, func(tx store.Operations) error)` and calls `tx`. `Operations` has no `InTx`, so a
  transaction cannot nest. What a rule decides on, it reads through `tx` too, and `InTx` promises those
  reads still hold at commit. `store/sqlite` keeps that by beginning immediate. From inside `fn`,
  `s.store` also deadlocks, because `:memory:` holds one connection.
- **Undo is a rule, so it is in `service`.** A store holds `previous_state` as text it never reads.
  `service/undo_records.go` owns that JSON. Entries are never pruned, so its shape is a format on disk,
  whatever types a store uses for its own rows. A new column on a project, item or task joins its record
  there and its store's restore, and `TestEveryModelFieldSurvivesAnUndo` fails until both carry it.
- **A store's statements carry what only a statement can do atomically, or what only it can sort.**
  `store/store.go` states each on its method: a new row's position, an item's `updated_at`, and a
  restore's handling of a row already there. Each store states them in its own `queries.sql`.

Two carve-outs are declared here rather than assumed:

- **`sync` reaches `store/sqlite/generated` directly.** A pull rewrites rows wholesale, with no undo entry
  and none of the rules, and the push queue and origin binding belong to no rule. Routing that through
  `service` would give the rules a surface only the engine calls. A pull writes only the columns it has
  always carried, so on a synced database `kind` and `completed_at` are what that database set, and neither
  arrives from elsewhere. It ends when the sync engine is deleted. `generated` can then move under
  `store/sqlite/internal`, where the compiler enforces the seam.
- **Database mode links `service` and `api/routes` directly.** A client normally depends on the wire
  contract and never on a server's types, because importing them couples deploy order. Database mode
  serves itself, so that reason does not reach it. `apiclient` speaks `api/wire` and converts to
  `model` for the board, and imports no route, service or store. The sync engine keeps its own lean
  structs.

## `projects` is a command line, and `projects tui` is the board

Bare `projects` prints help. The project verbs sit at the root, and the item verbs under
`projects items`, with `tasks`, `dependencies` and `memberships` as nouns under it. `cli/root.go`
builds the tree. Help renders through `gotermstyle/cobrahelp`.

- **The `api` key, or `--api`, chooses the mode, and neither means the database.** `decideMode` in
  `cli/data_source.go` reads goclilogin's parse of the key, so the mode and the login read one
  setting. It runs in each command's `RunE`, so `help`, `completion` and `version` open nothing.
- **`api` beside sync refuses**, through `chooseMode`, the one check every command, `projects sync`
  and `auth login` take. The two are two authorities for the same rows. The refusal names
  `PROJECTS_SYNC` when the environment turned sync on, and the config file's `[sync]` otherwise.
- **With sync on, a command that writes refuses before it asks anything.** `openForWriting` holds the
  sentence, and `refusingWrites` answers 409 with it for any write that reaches the in-process routes
  without it. A write there would land in the copy with nothing queued, and the next pull deletes it.
- **Exit codes are 0, 1 for refused or failed, 2 for usage, and 4 for no answer.** `exitCodeFor` in
  `cli/root.go` maps them, and `explain` in `cli/api_errors.go` words them. A 422 is usage.
  A refused credential is 1.
- **4 means running it again is safe.** That covers a read with no answer or a 5xx, a 429, a write
  that never left, and a token request the login provider did not answer. `apiclient.Retrying`
  returns `apiclient.Unanswered`, which records the method, the attempts made and whether any try
  was sent.
- **A write that was sent and got no answer, or got a 5xx, exits 1.** It may have been carried out,
  and the message says to check before running it again.
- **The token is fetched outside the retries.** `oauth2.Transport` wraps `Retrying`, so a failed token
  request is not retried as though the API had not answered. `failedTokensMarked` tags the failure
  as a `tokenFailure`, and `noToken` words it with the issuer or the `token_command`.
- **A `--json` document this tool composes carries `schema_version`**, and its schema is under
  `cli/schemas`. A new shape takes a file there and a row in `both_modes_test.go`. Every other
  `--json` answer is the API's own, under `api/schemas`.
- **Login is goclilogin**, configured in `cli/api_login.go`: the keyring service `projects`, the
  client id `projects-cli-<host>`, and the client-credentials grant with `projects.items.read` when
  `PROJECTS_CLIENT_SECRET` is set.
- **The repo-name ban runs in database mode only.** `LocalService` sets `RefusingRepoNames`, and an API
  sets none. `--repo` is checked against this machine's registry in database mode only too, because
  an API serves tags written from every machine. `repoRegistry` on the data source answers an empty
  registry in API mode.

An item shows its **number**, the handle the API assigns and answers with. The UUID is the sync key
and stops there.

The number is assigned by whichever database is the authority. Through an API, the API numbers it as
it creates it. With sync on that is the API, and
`pushCreatedItem` writes the number from the create response into the local row, so the TUI shows
it once the push lands rather than after the next pull. With sync off, `AssigningItemNumbers` makes
`projects` allocate `max+1` itself, which cannot collide because that database is a disjoint universe.

An item created while the API is unreachable has no number until its push lands, and shows its UUID
tail until then (`itemHandle` in `tui/app.go`).

## A project name is bounded work, never a repo

Creating a project refuses a name the repo registry knows, and so does a rename.
The test is whether the thing ENDS, not whether the name reads like a verb
phrase:

    "projects sync improvements"    OK — the sync improvements end
    "Extract xx from dotfiles"    OK
    "Migrate neovim to vim.pack"  OK
    "projects"                      banned — names a thing that exists

The failure it prevents: a repo gets a project while it is being BUILT, which is
finite and does complete. The repo then keeps existing, the next papercut has
nowhere else to go, and the finished effort silently becomes the eternal bucket.
The tell was dotfiles' own description, which had grown a hand-written BOUNDARY
paragraph explaining which work belonged to it — a modeling gap patched with
prose.

The repo association is the item's repo tag, which already crosses project
boundaries and outlives any single project. "What is the dotfiles work" is a
query on that tag, spanning live projects, finished ones, and whatever is filed
elsewhere.

A one-item project is the floor, not the target: fifteen papercuts are four or
five small thematic projects, not fifteen projects. If the ban produced one
project per item the list would read like items, which is the complaint that
started this.

A missing registry bans nothing.

Enforcement is on the service (`RefusingRepoNames`), the one path every create
and rename takes. It is an option `cli.LocalService` sets for database mode. `api/main.go` does not,
because a container holds no registry, so the API bans nothing, as a missing registry does. A
command through an API therefore bans nothing either.

## A project has a status, and it is not an `archived` flag

`projects.status` is `active`/`someday`/`completed`/`dropped`. For an item, complete and archive are
orthogonal; for a project they collapse, because a project is a finite effort with a definition of
done, so completion *is* the hide signal. `dropped` requires a reason: "deferred" invites
re-proposal, "dropped, and here is why" closes the question.

`someday` is shelved, not closed. It is hidden and gives up its name like a closed project, but
`closed_at` stays NULL, matching the API. So "is it closed" and "is it hidden" are different
questions: the TUI asks `status != active` for hiding and for offering reopen.

`service.SetProjectStatus` is the only rule that moves a status, and it derives `closed_at` and
`status_reason` from the transition rather than accepting them from a caller, so an active project
can never carry either. `UpdateProject` deliberately does not touch status.

**Closing a project does not cascade to its items.** An item still open when the project was
dropped WAS still open; "shipped 8 of 11, dropped with 3 open" is a real signal and eleven archived
items is not. Visibility is derived from the project instead.

**A name is held only by the active project bearing it.** That is
`idx_projects_name_active` in `indexes.sql`, a partial unique index, and it is
what lets a finished `clisteno` give the name back.

`migrateProjectStatus` is the one migration that rebuilds a table rather than
adding columns, because SQLite attaches the old column-level `UNIQUE(name)` as
an implicit index no DDL can drop. Foreign keys are off across the swap:
`project_item_memberships` cascades off `projects`, so dropping the old table
with them on would take every membership. Do not simplify it into three
`ALTER TABLE ADD COLUMN`s — leaving the global constraint in place fails the
entire pull transaction the first time the server returns a done project beside
a live one sharing its name.

## A database and its API are a pair — never mix a dev half with a prod half

`.envrc` exports two coupled variables, `PROJECTS_DB` and `PROJECTS_SYNC_URL`, and
`PROJECTS_SYNC_TOKEN_COMMAND` where the dev API wants a token. They name one
environment. A URL from the environment never takes the config file's
`token_command` (`config.Load`), so the dev URL cannot carry the production
token. Overriding one of them names an environment that does not exist, and
for this tool that is destructive rather than merely wrong: a pull deletes
every local row the server did not return, so
a production database reconciled against the dev API is emptied of everything
dev has never heard of. A sync run that way once deleted 42 projects and 286
items and printed `Synced.`

**To run against production data, run from outside this repo**, where direnv has
loaded nothing. Un-setting your way out is how the accident happened: `env -u`
on the variable you remembered still leaves the one you did not.

`sync_origin` is the enforcement, because a rule only prose enforces is
decoration. The first pull records which API the database belongs to, and every
pull and push after that refuses any other (`OriginMismatch`). `--adopt` is the
only way the pairing changes. A database that has pulled before but carries no
origin — anything predating this — refuses to guess and asks to be bound, since
inferring it from whichever environment is loaded *is* the mistake.

`guardSweep` is the second layer, for when the API is right but its answer is
not. A pull that would delete ten or more local projects, and more than half of
them, is refused, and items are guarded the same way. A truncated response, an
auth failure that still returns 200, and a genuine mass deletion are the same
event seen from here. `--force` allows it. The floor counts rows deleted, not
rows held, so 8 deletions out of 15 pass. It keeps the guard silent on small
databases, where losing most of it is a handful of rows; a guard that fires on
ordinary days gets forced past by reflex and stops guarding anything.

Neither override is reachable from the background timers. A guard something
automatic can waive is not a guard.

## Sync is automatic; `projects sync` is a convenience, not a requirement

`sync.interval` (default 2m, floored at 15s) is the single knob for how stale `projects` may ever be,
and it drives two loops:

- **TUI** — `syncPullTickMsg` re-arms itself every interval, and always reschedules, even on the
  ticks it skips: a tick that returns no command kills background sync for the session.
  `safeToAutoPull` gates the reconcile to `modeNormal`, because a pull rewrites items, memberships
  and ordering wholesale and would move the ground under a grab or a text entry.
- **Push** — `pushLoop` carries a retry ticker alongside `Notify`, which fires only on a local
  mutation, so a push that failed while the API was down retries without another edit.

An automatic pull neither flashes on success nor claims the status bar on failure; failure surfaces
through the engine status (`SYNC ERR`). Local-first means an unreachable API degrades to local
data, never an error.

`Pull` records a pending-sync high-water mark before it fetches, then clears only the ops at or
below it and spares entities queued above it from the "deleted upstream" sweeps. Without that mark,
an item typed mid-pull is deleted by the same pull and its queued create dropped with it. Do not
restore `DeleteAllPendingSync` here.

**Absent is not empty, and that is what keeps deploy order between this repo and its server
irrelevant.** The item list embeds memberships, dependencies and tasks; when the server omits
`dependency_ids`/`tasks`, the pull falls back to the per-item endpoints — decode into slices and
branch on nil, never on empty, or the first pull against an older API deletes every local task and
dependency. The project pull asks for `?status=all` because the sweep deletes any local project the
server did not return, so an active-only response would read "completed" as "deleted" and cascade
through the memberships. A server sending no project status is read as `active`. Do not drop either
fallback just because the API has shipped.

## The API is `service` over Postgres, and a handler holds no rule

`api/routes` decodes, calls `service` and encodes. A rule in a handler would have to land again for
SQLite. `service.Service` carries the request through `WithContext`, so a request's cancellation and
its `store.Writer` reach `InTx`.

```bash
go test ./api/...                                # routes, schemas and scopes, on SQLite with a stand-in issuer
go test ./store/postgres/                        # the audit trigger, on a real Postgres
PROJECTS_TEST_STORE=postgres go test ./service/  # every rule on Postgres
(cd store/postgres && go tool sqlc generate)     # after editing queries.sql or a migration
```

`postgrestest` starts one Postgres container per test binary with testcontainers, and clones a template
per test. Set `TEST_DATABASE_URL` to use a running server. `-short` skips them, and the commit hook runs
`-short`, so CI is what runs them.

- **`api/wire` is the contract, and `model` is not.** A client depends on the wire, so an answer changes
  only by editing a `wire` struct and its file under `api/schemas`. `TestEveryAnswerMatchesItsSchema`
  fails when the two differ. A new shape takes a file and a row in that test.
- **A row's every field is always sent: an empty value as `null`, and an empty list as `[]`. Times are UTC
  with a `Z`.** `wire.Time` prints six fractional digits when there is a fraction, and none when there is
  not. Existing clients index these by name.
- **`/api/v1` is this API's version signal, and no answer carries `schema_version`.** That is an exception
  to the rule that a shape names its version in the document, because a list answers a bare array, which
  has no place for the field. A field added is not a new version, and a field reshaped or removed is.
- **A `PATCH` is read field by field**, by `requestBody`, because a struct cannot tell a field left out
  from one sent as null. Left out keeps, null clears, and a column that cannot be empty refuses null.
- **A wildcard holds an escaped slash.** `{id}` matches one escaped segment, so a project name holding
  `/` is reached as `%2F`. A catch-all would widen what `LimitServices` matches by pattern.
- **A scope lists registered patterns**, in `routes.ServiceScopes`. Renaming a route's wildcard takes it
  out of the scope.
- **`InTx` takes `pg_advisory_xact_lock` first**, the analog of SQLite's immediate transaction, so what a
  rule read still holds at commit. It costs one writer at a time, which one person's write rate never
  feels. `WithRequestDeadline` bounds that wait, and every other, so a database that stops answering
  costs a client a 503.
- **Every write is audited by a row trigger**, into `audit_trail`, because a trigger also sees cascades.
  `InTx` binds `projects.actor`, `projects.request_id`, `projects.job_run_id`, `projects.traceparent` and
  `projects.baggage` with `set_config(..., true)`, and the trigger reads them. A person is recorded by
  subject and a service by client id. `undo_log` has no trigger: it writes on every write, and would copy
  each whole row the real tables already audit. A new table brings its trigger, or
  `TestEveryTableIsAuditedButWhatOptsOut` fails.
- **A kind or status is a row in `model.ProjectKinds` or `model.ProjectStatuses`**, and in SQLite's
  `lookup_rows.sql`. `postgres.Migrate` upserts Postgres's from `model`, and
  `TestEachStoreHoldsTheKnownKindsAndStatuses` holds both stores to it.
- **Item numbers come from the `item_numbers` sequence**, through `AssigningItemNumbers`. A database
  loaded from elsewhere needs the sequence set from the old sequence's own counter, because numbers it
  already handed out can outrun the rows left.
- **`.sqlfluffignore` keeps the SQLite hook off `store/postgres`**, and a second hook in
  `.pre-commit-config.yaml` lints it in its own dialect, because the config holds one dialect.
- **A release publishes the image and manifests at the tag `go-semantic-release` cut**, in `release.yml`.
  One version names both the TUI and the API, because a rule change in `service` moves both. A new
  directory the binary imports joins `.dockerignore`'s allowlist, or `validate.yml`'s image build fails
  the pull request.

## Never write the breaking-change trailer in a commit message

Those two words anywhere in a message cut a major here, and a major on a Go module
with no `/vN` path is an outage: `go install …@latest` stops seeing the tag, every
installed binary is stranded, and recovery is a reinstall on each machine. The
analyzer matches unanchored and ORs past `.semrelrc`, so nothing switches it off.
A commit that merely *discusses* the trailer cuts one too — say "that marker".
Deliberate majors are `chore(release-major)`, and the same release adds the
`/vN` suffix to the module path.

History cannot be rewritten on `main`, so a major cut by accident is reset in
this order:

1. Delete the vN GitHub releases and their tags, remote and local.
2. Tag a baseline on `main` above the offending commit, and push it. Without it
   the commit stays in the analyzed range and the next push cuts the major again.
3. Confirm `go list -m github.com/datapointchris/projects@latest` answers the v1 tag.
4. Reinstall on every machine. Self-update will not recover a stranded binary.
