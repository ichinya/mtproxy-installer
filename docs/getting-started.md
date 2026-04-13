[Back to README](../README.md) · [Configuration →](configuration.md)

# Getting Started

Текущий рекомендуемый install path для `mtproxy-installer` проходит через Go CLI `mtproxy`.
Основной first-class сценарий остаётся `telemt`-based deployment через `providers/telemt`.

## Что понадобится

- Linux host / VM / WSL с `sudo`
- Docker и Docker Compose plugin
- внешний IP, который будет использоваться как `public_host` / `announce`
- FakeTLS domain, если не устраивает default

## Быстрая установка

```bash
cd app
go build -o mtproxy ./cmd/mtproxy
sudo ./mtproxy install --provider telemt
```

По умолчанию команда:

- создаёт `/opt/mtproxy-installer`
- готовит provider layout
- генерирует secret
- пишет `.env`, `docker-compose.yml` и provider config
- подтягивает provider image
- запускает compose runtime
- для `telemt` пытается получить startup `tg://proxy`

## Переопределение значений

Примеры:

```bash
# telemt на 8443
sudo ./mtproxy install --provider telemt --port 8443

# telemt с кастомным FakeTLS domain
sudo ./mtproxy install --provider telemt --tls-domain habr.com

# telemt с явным proxy user
sudo ./mtproxy install --provider telemt --proxy-user public

# mtg
sudo ./mtproxy install --provider mtg
```

Чаще всего переопределяют:

- `--port`
- `--api-port`
- `--tls-domain`
- `--proxy-user`
- `--install-dir`

Полный список параметров: `go run ./app/cmd/mtproxy help`.

## Что появится на диске

После успешной установки структура выглядит так:

```text
/opt/mtproxy-installer/
  .env
  docker-compose.yml
  providers/
    telemt/
      telemt.toml
      data/
```

Для `mtg` вместо `telemt.toml` будет `providers/mtg/mtg.conf`.

## Как проверить runtime

Минимальная проверка:

```bash
go run ./app/cmd/mtproxy status
go run ./app/cmd/mtproxy link
go run ./app/cmd/mtproxy logs --tail 100 --follow
```

Ручная проверка через Docker Compose:

```bash
docker compose -f /opt/mtproxy-installer/docker-compose.yml \
  --project-directory /opt/mtproxy-installer \
  --env-file /opt/mtproxy-installer/.env ps

curl http://127.0.0.1:9091/v1/health
curl http://127.0.0.1:9091/v1/users
```

Acceptance checklist:

- контейнер запущен
- local API отвечает
- `mtproxy status` не падает
- `mtproxy link` даёт валидный link или понятный degraded summary

Не используйте Telegram calls как acceptance criterion.

## Обновление

```bash
sudo ./app/mtproxy update
```

Если бинарник ещё не собран:

```bash
cd app
go run ./cmd/mtproxy update
```

`update` берёт provider/image из уже установленного runtime, подтягивает новый image, перезапускает сервис и делает rollback при провале validation.

## Удаление

```bash
# полное удаление
sudo ./app/mtproxy uninstall --yes

# удалить runtime, но сохранить install dir
sudo ./app/mtproxy uninstall --yes --keep-data
```

Важно для v1:

- `uninstall` работает только в стратегии `telemt_only`
- для `mtg`, `official`, ambiguous state и env/runtime mismatch cleanup не запускается
- команда требует `--yes`

## Локальная разработка

Для local/dev path из корня репозитория:

```bash
make setup
make dev
make test
```

Для app-layer:

```bash
cd app
go test ./...
go build ./cmd/mtproxy
```

## Next

- [Configuration](configuration.md) — env vars и настройки `telemt.toml`
- [Reverse Proxy](reverse-proxy.md) — L4 routing и fallback backend
- [Troubleshooting](troubleshooting.md) — диагностика runtime и compose
