[← Providers](providers.md) · [Back to README](../README.md) · [Installation Strategy →](installation-strategy.md)

# Upstream Repositories

Этот документ - «карта источников» для будущих вариантов установки.

Его задача простая: чтобы через месяц или полгода не вспоминать, какие upstream-репозитории мы смотрели, зачем они нужны и что именно из них полезно для `mtproxy-installer`.

## Как читать этот документ

Каждый внешний repo разбирается по одной и той же схеме:

- что это вообще такое;
- это engine, Docker-wrapper, installer или reference;
- почему он важен для нашего проекта;
- что из него нужно взять;
- что нельзя обещать пользователю без дополнительной проверки.

## Приоритет для этого репозитория

### 1. `An0nX/telemt-docker`

- URL: `https://github.com/An0nX/telemt-docker`
- Класс: Docker-wrapper
- Статус у нас: основа первого рабочего install path

Почему важен:

- именно на него сейчас логично опирать стартовую реализацию installer-а;
- он уже упаковывает `telemt` в production-friendly контейнер;
- он лучше совпадает с нашей целевой архитектурой, чем старые one-liner обертки вокруг `mtg`.

Что берем:

- образ и layout контейнера;
- hardening defaults;
- минимальный Compose-friendly способ запуска;
- базовые рекомендации по `nofile`, read-only и capability set.

Что не надо путать:

- это не самостоятельный protocol engine;
- все protocol и config-level ограничения все равно надо проверять по `telemt/telemt`.

### 2. `telemt/telemt`

- URL: `https://github.com/telemt/telemt`
- Класс: engine
- Статус у нас: основной upstream engine для default provider

Почему важен:

- это реальный источник правды по конфигу и runtime behavior;
- у него есть `config.toml`, `config.full.toml`, docs, API и описание режимов;
- он покрывает именно те advanced-сценарии, ради которых репозиторий и смещается в сторону `telemt`.

Что берем:

- формат `telemt.toml`;
- `server.api` и link generation;
- `proxy_protocol`, `listen_unix_sock`, metrics, middle-proxy, upstreams;
- provider-specific ограничения и возможности.

Что проверять отдельно:

- `ad_tag` и monetization behavior;
- middle-proxy / direct-mode сценарии;
- media/CDN поведение;
- reverse proxy + `proxy_protocol` в реальном deployment.

### 3. `9seconds/mtg`

- URL: `https://github.com/9seconds/mtg`
- Класс: engine
- Статус у нас: planned alt-provider

Почему важен:

- самый очевидный второй provider для selector-а;
- большое количество существующих инструкций в интернете опираются именно на него;
- у него хороший reputational weight как у зрелого и давно используемого решения.

Что берем:

- альтернативный provider path;
- подход к простому FakeTLS deployment;
- идеи для provider-specific Docker profile;
- понимание компромиссов `mtg v2`.

Что надо помнить:

- `mtg v2` не надо рекламировать как provider с `ad_tag` parity;
- комментарии и issue вокруг него показывают, что media/CDN и direct-vs-middle differences надо тестировать отдельно.

### 4. `TelegramMessenger/MTProxy`

- URL: `https://github.com/TelegramMessenger/MTProxy`
- Класс: engine / reference
- Статус у нас: official reference, не default path

Почему важен:

- это canonical source по official semantics;
- он нужен для аккуратной документации по `@MTProxybot`, promoted channels и official expectations;
- он помогает не придумывать неверную модель `ad_tag` из воздуха.

Что берем:

- reference-поведение;
- термины и ограничения official path;
- понимание, где `official` отличается от `telemt` и `mtg`.

Что не надо делать:

- не строить вокруг него основной installer UX, если `telemt` уже лучше закрывает наш default deployment.

## Второй эшелон источников

### `seriyps/mtproto_proxy`

- URL: `https://github.com/seriyps/mtproto_proxy`
- Класс: engine

Зачем смотреть:

- это сильный reference по feature-rich MTProxy, promoted channels, FakeTLS и policy surface;
- полезен как дополнительный источник по `ad_tag`-related логике и продвинутым конфигам.

Почему не default:

- сложнее и тяжелее для первого install path;
- для нашего репозитория важнее сначала закрепить ясные provider-paths для `telemt` и `mtg`.

### `alexbers/mtprotoproxy`

- URL: `https://github.com/alexbers/mtprotoproxy`
- Класс: engine

Зачем смотреть:

- useful reference по `AD_TAG`, FakeTLS, metrics и `PROXY_PROTOCOL`;
- показывает, как другие реализации решают схожие задачи.

Почему не default:

- не так хорошо совпадает с нашим текущим курсом на `telemt`.

## Installer-ориентированные источники

### `nolaxe/install-MTProxy`

- URL: `https://github.com/nolaxe/install-MTProxy`
- Класс: installer

Что полезно:

- UX-идеи;
- простая подача one-command установки;
- способ структурировать понятные шаги для непрофильного пользователя.

Что не надо копировать вслепую:

- архитектурные упрощения;
- слишком оптимистичные обещания «все работает из коробки» без test matrix.

### `anten-ka/gotelegram_mtproxy`

- URL: `https://github.com/anten-ka/gotelegram_mtproxy`
- Класс: installer

Почему важен:

- именно с него исходно начиналась идея «переписать старый скрипт на новый provider path»;
- это хороший пример минималистичного installer UX вокруг Docker.

Почему он не должен быть архитектурной базой:

- это слишком узкий и упрощенный сценарий;
- он не фиксирует полноту современных provider-различий.

## Периферийные, но полезные источники

### `Medvedolog/luci-app-telemt`

- URL: `https://github.com/Medvedolog/luci-app-telemt`
- Класс: auxiliary UI / ecosystem project

Зачем помнить:

- это сигнал, что вокруг `telemt` уже строят отдельные UI и management-layer решения;
- полезно как индикатор реального спроса на `telemt` и его operational features.

## Что берем в наш репозиторий прямо сейчас

1. Default provider path строим вокруг `An0nX/telemt-docker` + `telemt/telemt`
2. `mtg` фиксируем как planned alt-provider, а не как abandoned idea
3. `official` держим как reference provider для docs и comparison
4. Installer и docs пишем так, чтобы потом можно было добавить selector без переписывания половины репозитория

## Что еще надо обязательно проверить позже

- реальное текущее состояние `ad_tag` в `telemt`;
- media/CDN и `dc_overrides` для `telemt` и `mtg`;
- `proxy_protocol` в сценариях `nginx stream` / Traefik / HAProxy;
- насколько стабильны порты `8443` и `9443` относительно `443`;
- что именно из official semantics нужно воспроизвести в provider selector UX.

## See Also

- [Providers](providers.md) - итоговая provider matrix и ограничения
- [Installation Strategy](installation-strategy.md) - как эти upstream choices влияют на roadmap installer-а
- [Troubleshooting](troubleshooting.md) - практические проблемы, которые надо сверять с upstream behavior
