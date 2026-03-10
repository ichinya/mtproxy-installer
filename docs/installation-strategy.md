# Installation Strategy

Этот документ описывает не текущую реализацию как таковую, а план того, как installer должен эволюционировать дальше.

## Базовое решение

Первый рабочий path делаем на базе:

- GitHub repo: `An0nX/telemt-docker`
- upstream engine: `telemt/telemt`
- current provider dir: `providers/telemt`

То есть «по умолчанию» мы сейчас не делаем selector и не пытаемся поддержать всё сразу. Сначала закрепляем один понятный и документированный путь.

## Почему не делать selector сразу

Причины:

- у providers разный формат secret и разные operational assumptions;
- `ad_tag`, middle-proxy, metrics, API и reverse-proxy behavior не одинаковы;
- если сделать selector слишком рано, получится запутанный installer без четкой contract-схемы.

Поэтому текущая последовательность такая:

1. стабилизировать `providers/telemt`;
2. собрать docs и troubleshooting;
3. добавить `providers/mtg`;
4. только потом включать provider selector в `install.sh`.

## Как должна выглядеть структура providers

Целевая структура:

```text
providers/
  README.md
  telemt/
    README.md
    .env.example
    docker-compose.yml
    telemt.toml.example
  mtg/
    README.md
    .env.example
    docker-compose.yml
    config.example
  official/
    README.md
    .env.example
    docker-compose.yml
```

Не все эти файлы обязаны существовать сразу, но именно к этому layout мы хотим прийти.

## Что должно лежать в каждом provider dir

Минимум:

- `README.md` с upstream repo, текущим статусом и caveats;
- example env/config;
- compose-файл или другая повторяемая точка входа;
- notes по reverse proxy, `ad_tag`, metrics и link generation.

## Контракт для будущего selector-а

Когда появится selector, он должен опираться на provider-specific contract, а не на набор случайных `if/else` в одном файле.

Желаемый contract:

- provider знает свой upstream repo;
- provider знает, какой image или binary использовать;
- provider знает, какой config template ему нужен;
- provider знает, как генерировать secret;
- provider знает, как формировать link или откуда его читать;
- provider документирует ограничения по `ad_tag`, media/CDN, reverse proxy и stats.

## План по `telemt`

Для `telemt` надо закрыть:

- `telemt.toml` template;
- `server.api` и startup link extraction;
- `tls_domain`, `mask`, `mask_host`, `mask_port`;
- `proxy_protocol`;
- troubleshooting по media/CDN и `dc_overrides`;
- reverse proxy examples;
- четкую документацию по тому, какие параметры пользователь должен менять сам.

## План по `mtg`

Для `mtg` надо отдельно зафиксировать:

- какой Docker path использовать;
- как генерировать FakeTLS secret;
- какие ограничения у `mtg v2` относительно `ad_tag`;
- какие параметры нужно выносить в env;
- какие reverse-proxy схемы реально протестированы.

## План по `official`

`official` нужен не как первый provider, а как reference или optional provider.

Что должно быть понятно заранее:

- как он стыкуется с `@MTProxybot`;
- где у него official semantics лучше документированы, чем у альтернатив;
- что именно имеет смысл поддерживать в installer, а что лучше оставить как documented reference.

## Матрица проверки перед включением selector-а

До появления selector-а должны быть хотя бы такие проверки:

- link generation для каждого provider;
- запуск на `443`, `8443`, `9443`;
- direct run и reverse-proxy сценарии;
- media/CDN поведение;
- `proxy_protocol` on/off;
- поведение `ad_tag` там, где оно вообще заявлено;
- понятный rollback path при смене provider.

## Что не надо делать

- не обещать одинаковую функциональность всем providers;
- не прятать provider-specific caveats в общий README;
- не смешивать reference docs и actual supported install path;
- не делать вид, что `ad_tag` или calls работают везде одинаково.
