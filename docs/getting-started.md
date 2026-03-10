[Back to README](../README.md) · [Configuration →](configuration.md)

# Getting Started

Этот документ описывает текущий рабочий путь установки для `mtproxy-installer`. На сегодня first-class сценарий только
один: Telemt-based deployment через `providers/telemt`.

## Что понадобится

Перед установкой подготовь:

- Linux-сервер с root-доступом или `sudo`
- внешний IP-адрес, который будет использоваться как `public_host` и `announce`
- домен для FakeTLS, если не хочешь оставлять дефолт `www.google.com`
- Docker и Docker Compose, если хочешь запускать проект из локального клона вручную

Installer умеет ставить Docker и Docker Compose сам, если их еще нет в системе.

## Быстрая установка

Самый короткий путь:

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo bash
```

По умолчанию installer:

- создает каталог `/opt/mtproxy-installer`
- готовит `providers/telemt`
- генерирует secret
- пишет рабочий `telemt.toml` в FakeTLS-режиме
- запускает контейнер через корневой `docker-compose.yml`
- пытается получить готовую `tg://proxy` ссылку из локального Control API

## Переопределение значений при установке

Если нужен другой порт или домен, переменные можно передать сразу:

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | \
  sudo env PORT=8443 TLS_DOMAIN=habr.com PROXY_USER=public bash
```

Чаще всего переопределяют:

- `PORT`
- `API_PORT`
- `TLS_DOMAIN`
- `PROXY_USER`
- `RUST_LOG`

Полный список и смысл переменных собран в [Configuration](configuration.md).

## Что появится на диске

После успешной установки структура выглядит так:

```text
/opt/mtproxy-installer/
  .env
  docker-compose.yml
  providers/
    telemt/
      .env
      docker-compose.yml
      telemt.toml
      data/
```

## Ручной запуск из локального клона

Если работаешь не через installer, а из локального репозитория, подготовь файлы вручную:

```bash
cp .env.example .env
cp providers/telemt/.env.example providers/telemt/.env
cp providers/telemt/telemt.toml.example providers/telemt/telemt.toml
mkdir -p providers/telemt/data/cache providers/telemt/data/tlsfront
sudo chown -R 65532:65532 providers/telemt/data
docker compose up -d
```

Тот же локальный путь теперь можно запускать через Makefile:

```bash
make setup
make dev
make test
```

Перед запуском обязательно проверь в `providers/telemt/telemt.toml`:

- `middle_proxy_nat_ip`
- `public_host`
- `announce`
- `tls_domain`

## Quick Commands

| Command                | Description                                              |
|------------------------|----------------------------------------------------------|
| `make setup`           | подготовить локальные env/config файлы и data-директории |
| `make dev`             | поднять локальный Docker Compose stack                   |
| `make build`           | провалидировать root и provider compose-конфиги          |
| `make test`            | выполнить shell syntax check и базовые smoke-checks      |
| `make lint`            | прогнать `shellcheck` для `install.sh`                   |
| `make docker-dev-down` | остановить локальный stack                               |

Для полного списка используй `make help`.

## Как проверить, что все поднялось

Минимальная проверка после старта:

```bash
# Проверка здоровья API
curl http://127.0.0.1:9091/v1/health

# Получение proxy links
curl http://127.0.0.1:9091/v1/users

# Просмотр логов
docker compose -f /opt/mtproxy-installer/docker-compose.yml \
  --project-directory /opt/mtproxy-installer \
  --env-file /opt/mtproxy-installer/.env logs -f telemt
```

Контрольный список:

- контейнер запущен: `docker compose ps`
- API отвечает только на `127.0.0.1:9091`
- получена `tg://proxy` ссылка
- порт доступен снаружи

Если трафик идет через reverse proxy, сразу переходи к [Reverse Proxy](reverse-proxy.md). Если есть проблемы с медиа,
SNI или `proxy_protocol`, смотри [Troubleshooting](troubleshooting.md).

## Обновление

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/update.sh | sudo bash
```

Скрипт сохраняет конфиг и секрет, подтягивает свежий образ и перезапускает контейнер.

## Удаление

```bash
# Полное удаление
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo bash

# С сохранением данных
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo env KEEP_DATA=true bash
```

## Следующие шаги

- настрой переменные окружения и `telemt.toml` под свой сервер
- реши, нужен ли тебе own-domain fallback вместо дефолтного `tls_domain`
- определи, будет ли deployment работать напрямую или за `nginx` / Traefik

## See Also

- [Configuration](configuration.md) - все ключевые env vars и настройки Telemt
- [Reverse Proxy](reverse-proxy.md) - схемы с L4-routing и fallback backend
- [Troubleshooting](troubleshooting.md) - частые проблемы после первого запуска
