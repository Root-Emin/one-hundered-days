# Contributing

## From clone to running, in one command

```sh
cd learning/go/Section-19-.../Day-93
./scripts/setup.sh
```

That runs the doctor, downloads modules, installs the pinned tools, applies the
migrations and runs the tests. Then:

```sh
make run            # the service on :8093
make check          # fmt, vet, lint, race - what CI runs
make help           # every target, with a description
```

If you prefer to do it yourself, the setup script contains no secrets — every
step is a make target.

## Every target

```
make help
```

`make` with no arguments prints that help, because a Makefile whose targets you
have to read the source to discover is not documentation.

| Target | What it does |
|---|---|
| `setup` | bootstrap a fresh clone: tools, modules, database |
| `doctor` | check whether this machine can build and run the project |
| `run` | start the service (applies migrations first) |
| `build` | compile into `dist/` |
| `test` / `test-race` / `cover` | the tests, three ways |
| `lint` / `fmt` / `vet` | the static checks |
| `check` | everything CI runs, locally, with the same flags |
| `migrate` / `migrate-down` / `migrate-status` | the database |
| `hooks` | install the pre-commit hook |
| `clean` | remove build output and the local database |

**`make check` is the one to remember.** It runs exactly what the pipeline runs,
so a green local check means a green build.

## When something does not work

```sh
make doctor
```

```
  ok    go version             go1.26.5
  ok    git                    /usr/bin/git
  warn  NOTES_ADDR             not set - the listen address; defaults to :8093
        fix: direnv allow (loads .envrc), or export it in your shell
  warn  database               notes.db does not exist yet
        fix: make migrate
```

Every line carries a **fix**, not a verdict. Warnings never stop you building or
testing; failures do.

## Configuration (direnv, optional)

`.envrc` sets `NOTES_ADDR`, `NOTES_DB` and `NOTES_LOG_LEVEL`. With
[direnv](https://direnv.net) installed, `direnv allow` loads it when you `cd`
in and unloads it when you leave — so your shell, your editor's terminal and
`make run` all see the same configuration. That mismatch is where "works on my
machine" usually comes from.

It stays optional: every variable has a default in the code, so the project
works without direnv installed.

**Secrets go in `.envrc.local`**, which is gitignored and sourced by `.envrc`.
Never put a real secret in a committed file.

## The pre-commit hook (optional)

```sh
make hooks     # git config core.hooksPath <day>/.githooks
```

`core.hooksPath` is what makes hooks shareable: `.git/hooks` is not
version-controlled, so a hook that lives there exists only on the machine where
someone remembered to copy it.

The hook runs `gofmt` on the **staged files only**, then `go vet` and
`go build` — about a second. It deliberately does **not** run the race detector
or the integration tests: a hook that takes thirty seconds gets bypassed with
`--no-verify` within a week, and then it protects nothing.

To remove it: `git config --unset core.hooksPath`.

## Migrations

```sh
make migrate           # apply pending
make migrate-status    # what is applied, and when
make migrate-down      # roll back one, and only one
```

Rules the runner enforces:

- **Every `.up.sql` needs a `.down.sql`.** `Load` refuses a migration without
  one, because a migration with no way back is a deploy with no way back.
- **Each migration and its bookkeeping row commit together.** A crash mid-way
  leaves the database at a known version, never half-applied.
- **`migrate-down` rolls back exactly one.** An accidental "roll everything
  back" against production is a data-loss incident; the extra keystrokes are
  the cheapest safety measure available.

Migrations are **embedded** in the binary (`internal/assets`), so the deployed
container does not need the repository checked out beside it — `make migrate`
and production run the same bytes.

## Adding a make target

Give it a `## name: description` line. `make help` reads those, and
`internal/makefilelint` fails the build if a target lacks one — along with
targets missing from `.PHONY`, a missing `.DEFAULT_GOAL`, and `@latest` tool
installs (which mean a lint failure nobody else can reproduce).

## The standard the DX aims at

Can someone who has never seen this repository get a running service without
asking anyone a question? Everything in this directory exists to make the
answer yes.
