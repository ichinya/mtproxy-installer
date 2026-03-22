# mtg provider

## Базовый upstream

- Engine: `https://github.com/9seconds/mtg`
- Docker image: `ghcr.io/9seconds/mtg:latest`

## Текущий статус

Это alt-provider, доступный через selector.

Файлы провайдера:
- `docker-compose.yml` — runtime определение
- `.env.example` — переменные окружения
- `mtg.conf.example` — шаблон конфигурации

## Как использовать

```bash
# Установка с mtg вместо telemt
PROVIDER=mtg curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo bash
```

## Генерация секрета

mtg использует FakeTLS секреты специального формата:

```bash
# Base64 secret
docker run --rm ghcr.io/9seconds/mtg:latest generate-secret www.google.com

# Hex secret (начинается с ee)
docker run --rm ghcr.io/9seconds/mtg:latest generate-secret --hex www.google.com
```

Для `mtg v2` в конфиге обязательны только `secret` и `bind-to`.
Публичный `IP:PORT` задается не через `advertise`, а через опубликованный Docker-порт и ссылку,
которую installer печатает после запуска.

## Почему он нам нужен

- это один из самых известных и распространенных MTProto/FakeTLS engines;
- он хорошо подходит для простого Docker deployment;
- он полезен как второй provider с другим operational profile.

## Отличия от telemt

| Характеристика | mtg | telemt |
|----------------|-----|--------|
| HTTP API | ❌ Нет | ✅ `/v1/users`, `/v1/health` |
| Генерация link | ❌ Вручную | ✅ Автоматически |
| ad_tag | ❌ Нет в v2 | ⚠️ Проверять |
| Secret формат | FakeTLS (спец.) | hex (32 символа) |

## Что важно помнить

- `mtg v2` нельзя считать provider-ом с полной поддержкой `ad_tag`;
- behavior по media/CDN надо проверять отдельно;
- нет HTTP API для автоматического извлечения proxy link;
- описывать его нужно как отдельный компромиссный путь, а не как drop-in replacement для `telemt`.

## Reverse Proxy

mtg поддерживает работу через reverse proxy, но поведение отличается от telemt:
- `proxy_protocol` может работать иначе;
- тестировать на конкретной конфигурации.
