#!/usr/bin/env bash
# One command from a fresh clone to a running service.
#
# Everything here is also a make target; this script exists so the README can
# say "run ./scripts/setup.sh" and mean it, including on a machine where the
# reader has not yet learned what the targets are.

set -euo pipefail

# Run from the day directory: ./scripts/setup.sh
cd "$(dirname "$0")/.."

say() { printf '\n\033[1m%s\033[0m\n' "$1"; }

say "1/5 checking the toolchain"
go run ./cmd/doctor

say "2/5 downloading modules"
go mod download

say "3/5 installing developer tools"
make tools || echo "  (tools are optional; lint will tell you if one is missing)"

say "4/5 applying migrations"
go run ./cmd/migrate -db "${NOTES_DB:-notes.db}" up

say "5/5 running the tests"
go test -count=1 ./...

say "done"
cat <<'MESSAGE'
  make run             start the service
  make check           everything CI runs
  make hooks           install the pre-commit hook
  make help            every target
MESSAGE
