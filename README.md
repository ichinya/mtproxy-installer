# mtproxy-installer

> Go-first CLI toolkit для развёртывания Telegram MTProto proxy через Docker Compose.

`mtproxy-installer` поднимает MTProto proxy через Docker Compose и поддерживает provider paths:

- `telemt` — основной рабочий path
- `mtg` — альтернативный provider path

Основной lifecycle install/update/uninstall теперь выполняется через Go CLI `app/cmd/mtproxy`.
Root `install.sh`, `update.sh` и `uninstall.sh` остаются legacy compatibility assets, но CLI больше не зависит от них.

## Быстрый старт

Предпочтительный путь:

```bash
cd app
go build -o mtproxy ./cmd/mtproxy
sudo ./mtproxy install --provider telemt
```

Примеры:

```bash
# telemt на 443
sudo ./mtproxy install --provider telemt

# telemt на 8443 с кастомным FakeTLS domain
sudo ./mtproxy install --provider telemt --port 8443 --tls-domain www.wikipedia.org

# mtg
sudo ./mtproxy install --provider mtg

# update
sudo ./mtproxy update

# uninstall
sudo ./mtproxy uninstall --yes
```

Если запуск идёт из корня репозитория:

```bash
go run ./app/cmd/mtproxy help
go run ./app/cmd/mtproxy status
```

## Что делает installer

- создаёт runtime в `/opt/mtproxy-installer`
- пишет `.env`, `docker-compose.yml` и provider config
- подтягивает provider image и фиксирует image/source contract
- запускает Docker Compose runtime
- для `telemt` пытается получить startup link через local Control API

## Операции

| Команда | Описание |
| --- | --- |
| `mtproxy install` | установка runtime через native Go lifecycle |
| `mtproxy update` | обновление provider image с validation и rollback |
| `mtproxy uninstall` | v1 `telemt-only` удаление runtime |
| `mtproxy status` | runtime summary по `.env`, compose и telemt API |
| `mtproxy link` | вывод полного `tg://proxy` только для подтверждённого runtime |
| `mtproxy logs` | raw `docker compose logs` для provider service |
| `mtproxy restart` | controlled restart + post-check |

Важно:

- `uninstall` в v1 работает только для `telemt`.
- Telegram calls не считаются supported acceptance criterion.
- structured output redacts secret/link; полный link печатается только через `mtproxy link`.

## Локальная разработка

```bash
make setup
make dev
make test

cd app
go test ./...
go build ./cmd/mtproxy
```

## Документация

| Раздел | Описание |
| --- | --- |
| [Getting Started](docs/getting-started.md) | install path, first checks, update и uninstall |
| [Configuration](docs/configuration.md) | env vars и ключевые параметры `telemt.toml` |
| [Providers](docs/providers.md) | provider matrix и ограничения |
| [Installation Strategy](docs/installation-strategy.md) | эволюция installer architecture |
| [Reverse Proxy](docs/reverse-proxy.md) | `nginx stream` и Traefik TCP examples |
| [Troubleshooting](docs/troubleshooting.md) | runtime diagnostics и частые проблемы |
| [App CLI README](app/README.md) | контракт Go CLI и его ограничения |

## Legacy shell entrypoints

В репозитории всё ещё лежат:

- `install.sh`
- `update.sh`
- `uninstall.sh`

Но актуальное поведение lifecycle нужно развивать в Go CLI (`app/internal/cli` и `app/internal/scripts`), а не в shell-wrapper path.
