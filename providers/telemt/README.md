# telemt provider

## Базовый upstream

- Docker wrapper: `https://github.com/An0nX/telemt-docker`
- Engine: `https://github.com/telemt/telemt`

## Текущий статус

Это текущий default provider для `mtproxy-installer`.

Сейчас именно на этом provider строятся:

- `install.sh`
- root `docker-compose.yml`
- `providers/telemt/docker-compose.yml`
- `providers/telemt/telemt.toml.example`

## Какой образ сейчас используется

По умолчанию мы используем:

`whn0thacked/telemt-docker:latest`

Это published image для репозитория `An0nX/telemt-docker`.

## Почему выбран именно он

- это наиболее логичный стартовый path по результатам изучения статьи, комментариев и upstream-репозиториев;
- `telemt` лучше соответствует целям проекта, чем старые one-liner пути вокруг `mtg`;
- Docker wrapper уже дает удобную упаковку для installer-а.

## Что еще нужно доделать

- оформить `mask_host` и `mask_port` как понятный optional path;
- отдельно покрыть `proxy_protocol`;
- вынести/проверить `dc_overrides` для media/CDN сценариев;
- расширить docs по reverse proxy;
- проверить текущее состояние `ad_tag` и middle-proxy behavior.

## Что нельзя обещать без тестов

- полную parity с official MTProxy по `ad_tag`;
- одинаковое поведение на всех портах и у всех провайдеров доступа;
- поддержку calls как штатной функции.
