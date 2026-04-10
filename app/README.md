# Go CLI bootstrap (`app/`)

This directory contains the initial Go application skeleton for the future `mtproxy` CLI.

Current scope:
- bootstrap-only command entrypoint (`cmd/mtproxy`)
- shared root command routing in `internal/cli`
- build metadata in `internal/version`
- startup logging with redaction-safe defaults

Important boundary:
- The active runtime path is still Bash-first (`install.sh`, `update.sh`, `uninstall.sh`).
- The Go CLI does not replace current operational flows yet.

## Local development

From repository root:

```bash
cd app
go test ./...
go build ./cmd/mtproxy
```

Run the binary:

```bash
./mtproxy help
./mtproxy version
```

## Build metadata injection

You can inject version fields with `-ldflags`:

```bash
go build -ldflags "\
  -X 'mtproxy-installer/app/internal/version.Version=1.0.0' \
  -X 'mtproxy-installer/app/internal/version.Commit=abcdef0' \
  -X 'mtproxy-installer/app/internal/version.BuildDate=2026-04-10T12:00:00Z' \
  -X 'mtproxy-installer/app/internal/version.BuildMode=production'" \
  ./cmd/mtproxy
```

Defaults without injection:
- `Version=dev`
- `Commit=unknown`
- `BuildDate=unknown`
- `BuildMode=development` (or inferred from version)

## Logging behavior

Logging is initialized at command boundary in `internal/cli.Execute`:
- startup log (`cli startup`) with binary name, argument count, startup mode
- resolved build metadata (`resolved build info`)
- selected subcommand (`selected subcommand`)
- dispatch lifecycle (`command dispatch start`/`finish`)
- fatal configuration errors (for example invalid `MTPROXY_LOG_LEVEL`)

Log level defaults:
- development build -> `DEBUG`
- production build -> `INFO`
- override via `MTPROXY_LOG_LEVEL` (`debug`, `info`, `warn`, `error`)

Redaction rule:
- non-`link` commands must not emit full proxy links or secret-like key/value data
- full proxy links are only allowed when the command is explicitly `link`

