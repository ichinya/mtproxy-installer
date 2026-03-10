# Reverse Proxy

Этот документ собирает выводы из переписки про `telemt` как default provider, FakeTLS и L4-routing поверх MTProxy.

## Зачем это нужно

Цель не в "магической невидимости", а в том, чтобы прокси выглядел как обычный HTTPS-трафик и был устойчивее к DPI.

Базовая схема:

```text
Telegram client
  -> Fake TLS handshake
  -> reverse proxy with SNI routing
  -> telemt / MTProxy backend
  -> Telegram servers
```

## Вариант 1: nginx stream

Используется L4-routing через `stream` и `ssl_preread`.

Что важно:

- это конфиг именно для `stream`, а не для `http`;
- proxy-domain вроде `tg.example.com` уходит на backend MTProxy/Telemt;
- остальной TLS-трафик уходит на обычный HTTPS backend;
- пример лежит в `examples/nginx/stream-multiplex.conf`.

Минимальная идея такая:

```nginx
map $ssl_preread_server_name $backend_dispatcher {
    tg.example.com 127.0.0.1:600;
    default        127.0.0.1:4443;
}

server {
    listen 443;
    proxy_pass $backend_dispatcher;
    ssl_preread on;
    proxy_connect_timeout 5s;
    proxy_timeout 1h;
}
```

## Вариант 2: Traefik TCP + HostSNI

Эквивалентный вариант для Traefik - TCP router с `HostSNI(...)` и TLS passthrough.

Что важно:

- это TCP routing, а не HTTP routing;
- proxy-domain маршрутизируется на backend с MTProxy/Telemt;
- catch-all может отправляться на обычный HTTPS backend;
- пример лежит в `examples/traefik/dynamic-tcp-router.yml`.

## Fallback-сайт

В переписке отдельно обсуждался fallback backend для обычного HTTPS.

Типовой смысл такой:

- прокси-поддомен обслуживается MTProxy/Telemt;
- обычные TLS-запросы получают легитимный сайт;
- в конфиге `telemt` это может соответствовать `mask_host` и `mask_port`.

Минимальный backend можно поднять на отдельном локальном порту, например `127.0.0.1:4443` или `127.0.0.1:601`.

## `proxy_protocol`

Это отдельная тема, которую нельзя смешивать с базовым SNI-routing.

- если reverse proxy передает соединение в backend через PROXY protocol, backend должен уметь его принимать;
- для `telemt` эту часть нужно отдельно проверять и документировать в provider-specific конфиге;
- если между роутером и прокси появляется промежуточный hop, важно явно понимать, где PROXY protocol "заворачивается" и где "разворачивается".

Именно поэтому reverse-proxy примеры вынесены в `examples/`, а не спрятаны в корневой `docker-compose.yml`.

## Где это должно жить в репозитории

- `examples/nginx/stream-multiplex.conf` - пример для `nginx stream`;
- `examples/traefik/dynamic-tcp-router.yml` - пример для Traefik TCP;
- `docs/providers.md` - ограничения и ожидания по провайдерам;
- issues - место для acceptance criteria и тестовой матрицы.
