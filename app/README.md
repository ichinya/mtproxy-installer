# Go CLI app-layer (`app/`)

`app/` содержит Go CLI `mtproxy` как дополнительный операторский слой над существующим Bash-first runtime.

Ключевая граница:
- `install.sh`, `update.sh`, `uninstall.sh` остаются базовым runtime contract.
- Go CLI не переопределяет эту модель, а добавляет единый интерфейс операторских команд.

## Scope и ограничения

- CLI сейчас telemt-first: полный happy path ориентирован на runtime `telemt`.
- Provider selector как first-class UX в CLI отложен.
- `status` и `link` для non-telemt runtime работают в partial/unsupported режиме с явными `WARN`.
- `uninstall` в v1 поддерживает только `telemt` (`telemt_only`) и требует `--yes`.
- Calls остаются non-goal для проекта и для CLI.

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
./mtproxy install --provider telemt
./mtproxy update
./mtproxy uninstall --yes --install-dir /opt/mtproxy-installer
```

## Поддерживаемые команды

| Команда | Назначение | Нюанс поддержки |
| --- | --- | --- |
| `help` | показать список команд | operator-safe |
| `version` | вывести build metadata | operator-safe |
| `status` | сводка runtime по `.env`, compose и telemt API | telemt-first; non-telemt => partial/unsupported summary |
| `link` | вывести proxy link | полный `tg://proxy` печатается только здесь, только в `stdout` и только при `compose=running`; иначе команда возвращает degraded summary |
| `logs` | стрим compose-логов сервиса провайдера | raw поток (может быть чувствительным) |
| `restart` | controlled restart + post-check | при деградации постпроверки возвращает операторский `WARN` |
| `install` | lifecycle wrapper для `install.sh` | telemt-first; structured output скрывает secret/link |
| `update` | lifecycle wrapper для `update.sh` | работает по уже установленному runtime; selector parity отложен |
| `uninstall` | lifecycle wrapper для `uninstall.sh` | v1 `telemt_only`, обязателен `--yes`, ранний отказ для mismatch/unsupported |

## Логирование

CLI пишет structured lifecycle-логи в `stderr`:
- startup/build info: `cli startup`, `resolved build info`, `selected subcommand`;
- command lifecycle: `... command entry`, `... lifecycle begin/finish`, `final runtime summary`;
- конфигурационные сбои: `fatal configuration error`.

Уровни:
- dev build: `DEBUG` по умолчанию;
- production build: `INFO` по умолчанию;
- override: `MTPROXY_LOG_LEVEL=debug|info|warn|error`.

Операторская интерпретация:
- `INFO` — штатное выполнение и успешный этап.
- `WARN` — ожидаемая деградация/caveat (unsupported provider fallback, link unavailable, restart degraded, uninstall без подтверждения).
- `ERROR` — команда не смогла завершиться корректно.

## Граница чувствительного вывода

- `mtproxy link` — явный путь полного proxy link в `stdout`, но только для подтверждённого runtime; при degraded/unverified compose raw link намеренно не печатается.
- `mtproxy logs` — raw контейнерный поток; вывод может содержать чувствительные данные.
- `status`/`install`/`update`/`restart`/`uninstall` и structured-логи соблюдают redaction-политику.
- Для `logs` structured logging не дублирует raw `stderr_summary` контейнера.

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
- `BuildMode=development` (или inferred from version)
