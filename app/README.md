# Go CLI app-layer (`app/`)

`app/` содержит Go CLI `mtproxy` и теперь является основным lifecycle entrypoint для `install`, `update` и `uninstall`.
Shell-скрипты в корне репозитория можно считать legacy compatibility path, но CLI больше не вызывает их во время runtime-команд.
Перед destructive/runtime lifecycle-командами CLI выполняет preflight: проверяет OS, root privileges, наличие `docker`, `docker compose` и доступность Docker daemon, а при ошибке возвращает actionable hint.

## Scope и ограничения

- CLI остаётся `telemt`-first: полный happy path ориентирован на runtime `telemt`.
- `providers/mtg` поддерживается для install/update path, но часть operator UX всё ещё проще, чем у `telemt`.
- `uninstall` в v1 поддерживает только `telemt` (`telemt_only`) и требует `--yes`.
- `status` и `link` для non-telemt runtime работают в partial/unsupported режиме с явными `WARN`.
- Telegram calls остаются non-goal для всего проекта, включая CLI.

## Локальная разработка

```bash
cd app
go test ./...
go build ./cmd/mtproxy
go run ./cmd/mtproxy help
```

Запуск бинарника:

```bash
./mtproxy help
./mtproxy version
./mtproxy status
./mtproxy link
./mtproxy logs --tail 50 --follow
./mtproxy restart
sudo ./mtproxy install --provider telemt
sudo ./mtproxy update
sudo ./mtproxy uninstall --yes --install-dir /opt/mtproxy-installer
```

## Поддерживаемые команды

| Команда | Назначение | Нюанс поддержки |
| --- | --- | --- |
| `help` | показать список команд | operator-safe |
| `version` | вывести build metadata | operator-safe |
| `status` | сводка runtime по `.env`, compose и telemt API | `telemt`-first; non-telemt => partial/unsupported summary |
| `link` | вывести proxy link | полный `tg://proxy` печатается только в `stdout` и только при подтверждённом runtime |
| `logs` | стрим compose-логов сервиса провайдера | raw поток, может содержать чувствительные данные |
| `restart` | controlled restart + post-check | при деградации постпроверки возвращает operator `WARN` |
| `install` | установить runtime через native Go lifecycle | structured output скрывает secret/link |
| `update` | обновить уже установленный runtime через native Go lifecycle | использует provider/image из текущего runtime |
| `uninstall` | удалить runtime через native Go lifecycle | v1 `telemt_only`, обязателен `--yes` |

## Логирование

CLI пишет structured lifecycle-логи в `stderr`:

- startup/build info: `cli startup`, `resolved build info`, `selected subcommand`
- command lifecycle: `... command entry`, `... lifecycle begin/finish`, `final runtime summary`
- конфигурационные сбои: `fatal configuration error`

Уровни:

- dev build: `DEBUG` по умолчанию
- production build: `INFO` по умолчанию
- override: `MTPROXY_LOG_LEVEL=debug|info|warn|error`

## Граница чувствительного вывода

- `mtproxy link` — единственный целевой путь полного proxy link в `stdout`, и только когда runtime подтверждён.
- `mtproxy logs` — raw контейнерный поток; считайте его чувствительным.
- `status`, `install`, `update`, `restart`, `uninstall` и structured-логи придерживаются redaction-политики.

## Build metadata injection

```bash
go build -ldflags "\
  -X 'mtproxy-installer/app/internal/version.Version=1.0.0' \
  -X 'mtproxy-installer/app/internal/version.Commit=abcdef0' \
  -X 'mtproxy-installer/app/internal/version.BuildDate=2026-04-10T12:00:00Z' \
  -X 'mtproxy-installer/app/internal/version.BuildMode=production'" \
  ./cmd/mtproxy
```

Значения по умолчанию:

- `Version=dev`
- `Commit=unknown`
- `BuildDate=unknown`
- `BuildMode=development`
