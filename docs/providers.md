[← Configuration](configuration.md) · [Back to README](../README.md) · [Upstream Repositories →](upstream-repositories.md)

# Providers

Этот документ фиксирует текущую стратегию репозитория по провайдерам.

Главное решение на текущий момент:

- первый рабочий install path делаем на базе `An0nX/telemt-docker`;
- фактический engine под ним - `telemt/telemt`;
- `mtg` и official MTProxy пока остаются запланированными вариантами для будущего selector-а;
- все внешние репозитории и компромиссы фиксируем сразу, чтобы потом не восстанавливать контекст по комментариям, issue и чатам.

## Текущее решение

Сейчас репозиторий должен исходить из такой связки:

- default provider: `telemt`;
- default wrapper: `An0nX/telemt-docker`;
- default Docker image: `whn0thacked/telemt-docker:latest`;
- current provider dir: `providers/telemt`.

Почему именно так:

- в статье и особенно в комментариях Habr именно `telemt` / `telemt-docker` чаще рекомендуют как более актуальный путь, чем старый `mtg` one-liner;
- `telemt` лучше подходит под цели репозитория: FakeTLS, masking, reverse proxy, `proxy_protocol`, metrics и локальный Control API;
- `An0nX/telemt-docker` уже упаковывает этот engine в удобный Docker-формат для installer-а.

## Классы источников

Чтобы не смешивать разные сущности, в репозитории используем такие термины:

- `engine` - собственно реализация MTProxy/MTProto proxy;
- `docker wrapper` - контейнерная упаковка engine-а;
- `installer` - скрипт или manager, который ставит и конфигурирует provider;
- `reference` - полезный upstream для поведения, форматов и ограничений, но не обязательно лучший default path.

## Матрица провайдеров

| Provider | Upstream repos | Роль в этом репозитории | Сильные стороны | Ограничения и риски |
| --- | --- | --- | --- | --- |
| `telemt` | `telemt/telemt`, `An0nX/telemt-docker` | текущий default provider | FakeTLS, masking, `proxy_protocol`, API, metrics, reverse-proxy fit | `ad_tag` и middle-proxy режим надо проверять по версии и конфигу, нельзя обещать parity с official MTProxy |
| `mtg` | `9seconds/mtg` | planned alt-provider | зрелый, простой, LB-friendly, хорошо известен в сообществе | в `v2` нет `ad_tag`, есть практические вопросы по медиа/CDN и test matrix |
| `official` | `TelegramMessenger/MTProxy` | reference / optional future provider | reference по `@MTProxybot`, promoted channels, official behavior | DX и Docker path слабее, docs и эксплуатация менее дружелюбны |

## Подробно по текущему default path

### `telemt/telemt`

Это основной engine, вокруг которого мы строим архитектуру.

Что нам от него нужно:

- TLS-only / FakeTLS режим;
- `mask`, `mask_host`, `mask_port`;
- `tls_domain`;
- `proxy_protocol`;
- `server.api`;
- startup links и `/v1/users` для вывода `tg://proxy` ссылок;
- metrics и runtime visibility.

Что надо помнить:

- `telemt` - это не просто «ещё один MTProxy», а достаточно самостоятельный и конфигурируемый engine;
- из-за этого installer должен оставаться минимальным, а advanced options - жить в provider-specific файлах и docs;
- часть практических знаний по нему сейчас идет не из одного README, а из upstream docs, комментариев и пользовательских кейсов.

### `An0nX/telemt-docker`

Это не отдельный engine, а именно Docker-wrapper для `telemt`.

Зачем он нужен:

- distroless runtime;
- non-root контейнер;
- multi-arch;
- уже подготовленный Compose-friendly путь;
- разумные hardening defaults.

Именно поэтому стартовый install path сейчас должен быть не «абстрактный telemt», а конкретно «telemt через `An0nX/telemt-docker`». Это позволяет прямо зафиксировать источник, документацию и future update path.

## Planned providers

### `mtg`

`mtg` нужен как второй provider, а не как замена текущему default path.

Он важен потому что:

- очень распространен в старых гайдах и one-liner инструкциях;
- зрелый и понятный для Docker deployment;
- хорошо подходит для простого FakeTLS сценария;
- вокруг него уже много примеров и статей.

Но при этом:

- в `mtg v2` нельзя рассчитывать на `ad_tag`;
- нельзя обещать одинаковое поведение по media/CDN относительно других engines;
- installer должен описывать его как отдельный provider со своими компромиссами.

### `official`

Official MTProxy нужен как reference provider.

Он полезен для:

- `@MTProxybot`;
- official middle-proxy semantics;
- promoted channels / `ad_tag` behavior;
- понимания форматов `proxy-secret`, `proxy-multi.conf` и базовых статистик.

Но как default installer path он сейчас не приоритетен.

## Что важно про `ad_tag`

`ad_tag` не означает, что любой proxy engine умеет «показывать свою рекламу» одинаково и без специальных условий.

Надо фиксировать это очень аккуратно:

- promoted channel получается через `@MTProxybot`;
- поведение зависит от middle-proxy flow;
- direct mode и разные реализации поддерживают это по-разному;
- для `mtg v2` нельзя считать `ad_tag` штатной возможностью;
- для `telemt` это нужно проверять по версии, режиму и фактическому поведению API/runtime.

Безопасная формулировка для README и issue:

> Регистрация, promoted channel, статистика и monetization-поведение зависят от выбранной реализации MTProxy и ее режима работы. Нельзя обещать одинаковую поддержку `ad_tag` для всех providers.

## Практические выводы из статьи и комментариев

Сильные сигналы, которые обязательно учитываем в docs и future implementation:

- `443` остается лучшим портом для легитимного внешнего вида трафика;
- `8443` и `9443` допустимы как fallback, но хуже с точки зрения мимикрии;
- `nginx stream` и Traefik TCP `HostSNI` - не «экзотика», а реальные сценарии, которые надо поддерживать docs-ами;
- `proxy_protocol` - частая точка ошибок, особенно когда reverse proxy и backend включают его не в тех местах;
- media/CDN и `dc_overrides` - отдельная практическая тема, которую нельзя оставлять только в комментариях Habr;
- звонки нельзя обещать как supported use case MTProxy.

## Куда смотреть дальше

- общий обзор upstream-репозиториев: `docs/upstream-repositories.md`
- стратегия future selector-а: `docs/installation-strategy.md`
- практические кейсы и проблемы из Habr: `docs/troubleshooting.md`
- provider-specific notes: `providers/README.md`

## See Also

- [Configuration](configuration.md) - переменные окружения и ключевые параметры Telemt
- [Upstream Repositories](upstream-repositories.md) - откуда берутся provider decisions
- [Installation Strategy](installation-strategy.md) - как текущий default path должен эволюционировать дальше
