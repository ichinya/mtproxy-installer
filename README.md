# mtproxy-installer

> Bash-first installer для Telegram MTProto proxy deployments с поддержкой выбора провайдера.

`mtproxy-installer` помогает быстро поднять MTProto proxy через Docker Compose. Текущие поддерживаемые провайдеры:
- **telemt** (default) — `An0nX/telemt-docker` + `telemt/telemt` engine
- **mtg** — `9seconds/mtg` FakeTLS engine

## Быстрый старт

```bash
# telemt (default)
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo bash

# telemt on 8443 with custom FakeTLS domain
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env TLS_DOMAIN=www.wikipedia.org bash -s -- telemt 8443

# mtg
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env PROVIDER=mtg bash

# mtg on 8443 with custom FakeTLS domain
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env TLS_DOMAIN=www.wikipedia.org bash -s -- mtg 8443

# telemt via env-only override
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env TLS_DOMAIN=www.wikipedia.org PORT=4321 bash
```

## Почему это полезно

- **Один рабочий install path** - стартовая установка строится вокруг `An0nX/telemt-docker` и `telemt/telemt`
- **Готовый Docker Compose layout** - installer создает структуру под `providers/telemt` и локальный Control API
- **Быстрый выход на `tg://proxy`** - после запуска installer пытается получить готовую ссылку из API `telemt`
- **Практические deployment notes** - в репозитории уже есть reverse-proxy примеры, provider strategy и troubleshooting

## Что этот installer не обещает

- голосовые звонки Telegram нельзя считать supported use case для MTProto proxy path;
- успешная установка означает доступ к Telegram, media и локальному Control API, но не гарантию рабочих calls;
- если calls являются жестким требованием, это нужно проверять отдельным сетевым путем, а не считать дефектом installer-а по умолчанию.

## Пример

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | \
  sudo env PORT=8443 TLS_DOMAIN=habr.com PROXY_USER=public bash
```

Этот запуск оставляет основной путь на `telemt`, но меняет внешний порт, TLS-домен и имя пользователя для ссылки прокси.

Если нужен явный выбор провайдера в одну строку:

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env TLS_DOMAIN=www.wikipedia.org bash -s -- telemt 8443
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env TLS_DOMAIN=www.wikipedia.org bash -s -- mtg 8443
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo env TLS_DOMAIN=www.wikipedia.org PORT=4321 bash
```

---

## Документация

| Раздел                                                 | Описание                                                 |
|--------------------------------------------------------|----------------------------------------------------------|
| [Getting Started](docs/getting-started.md)             | Установка, ручной запуск и первая проверка               |
| [Configuration](docs/configuration.md)                 | Переменные окружения и ключевые параметры `telemt.toml`  |
| [Providers](docs/providers.md)                         | Стратегия по провайдерам и границы текущего default path |
| [Upstream Repositories](docs/upstream-repositories.md) | Карта внешних репозиториев и их роль                     |
| [Installation Strategy](docs/installation-strategy.md) | План эволюции installer-а и будущего selector-а          |
| [Reverse Proxy](docs/reverse-proxy.md)                 | Схемы с `nginx stream` и Traefik TCP                     |
| [Troubleshooting](docs/troubleshooting.md)             | Практические проблемы и рабочие обходы                   |

## Дополнительно

- [providers/README.md](providers/README.md) - соглашения для provider-oriented layout
- [providers/telemt/README.md](providers/telemt/README.md) - заметки по текущему default provider
- [providers/mtg/README.md](providers/mtg/README.md) - план по альтернативному provider path
- [providers/official/README.md](providers/official/README.md) - reference notes по official stack

## Операции

| Скрипт         | Описание                                          |
|----------------|---------------------------------------------------|
| `install.sh`   | Установка с нуля                                  |
| `update.sh`    | Обновление provider image с сохранением secret/config и rollback при неуспехе |
| `uninstall.sh` | Удаление контейнера, образа и данных              |

```bash
# Обновление
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/update.sh | sudo bash

# Удаление
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo bash

# Удаление с сохранением данных
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo env KEEP_DATA=true bash
```

## Локальная разработка

| Command            | Description                                                    |
|--------------------|----------------------------------------------------------------|
| `make setup`       | создать локальные `.env` и `telemt.toml` из example-файлов     |
| `make dev`         | поднять локальный Telemt stack через root `docker-compose.yml` |
| `make test`        | прогнать shell smoke-checks и проверить compose-конфиги        |
| `make lint`        | проверить `install.sh` через `shellcheck`                      |
| `make build`       | провалидировать root/provider Compose manifests                |
| `make docker-logs` | посмотреть логи контейнера `telemt`                            |

Запусти `make help`, чтобы увидеть полный список targets.

## Лицензия

Лицензия в репозитории пока не указана.

## CLI Status and Link (Read-only)

The Go CLI now provides two read-only runtime inspection commands:

```bash
cd app
go run ./cmd/mtproxy status
go run ./cmd/mtproxy link
```

Behavior summary:
- `status` reconciles `.env`, `docker compose ps`, `/v1/health`, and `/v1/users` into a runtime summary.
- `link` prints a full `tg://proxy` link only to command stdout when telemt runtime resolution succeeds.
- non-telemt runtimes are reported as partial/unsupported with explicit WARN diagnostics.
- logs keep proxy links redacted; full links are never logged by `status`.
