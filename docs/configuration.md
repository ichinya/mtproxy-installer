[← Getting Started](getting-started.md) · [Back to README](../README.md) · [Providers →](providers.md)

# Configuration

Этот документ собирает настройки, которые чаще всего меняют при Telemt-based deployment. Он покрывает root-level `.env`,
provider-level параметры и ключевые поля `providers/telemt/telemt.toml`.

## Root `.env`

В корне репозитория используется небольшой набор переменных:

| Variable       | Default                            | Purpose                                |
|----------------|------------------------------------|----------------------------------------|
| `PORT`         | `443`                              | Внешний порт, который публикует Docker |
| `API_PORT`     | `9091`                             | Локальный порт Control API             |
| `TELEMT_IMAGE` | `whn0thacked/telemt-docker:latest` | Образ из `An0nX/telemt-docker`         |
| `RUST_LOG`     | `info`                             | Уровень логирования контейнера         |

## Переменные installer-а

Во время установки дополнительно используются:

| Variable       | Typical value            | Purpose                                  |
|----------------|--------------------------|------------------------------------------|
| `INSTALL_DIR`  | `/opt/mtproxy-installer` | Каталог установки                        |
| `PORT`         | `443` или `8443`         | Порт, на котором слушает прокси          |
| `API_PORT`     | `9091`                   | Публикация локального API на `127.0.0.1` |
| `TELEMT_IMAGE` | published image          | Какой контейнер запускать                |
| `TLS_DOMAIN`   | `www.google.com`         | FakeTLS-домен                            |
| `RUST_LOG`     | `info`                   | Runtime verbosity                        |
| `PROXY_USER`   | `main`                   | Имя пользователя для startup link        |

Пример передачи переменных прямо в installer:

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | \
  sudo env PORT=8443 TLS_DOMAIN=habr.com PROXY_USER=public bash
```

## Ключевые секции `telemt.toml`

Шаблон лежит в `providers/telemt/telemt.toml.example`. Ниже перечислены параметры, которые важно проверить перед
production-запуском.

### `[general]`

| Key                   | Default                        | Meaning                                        |
|-----------------------|--------------------------------|------------------------------------------------|
| `use_middle_proxy`    | `true`                         | Включает middle-proxy path                     |
| `proxy_secret_path`   | `/var/lib/telemt/proxy-secret` | Путь к secret внутри контейнера                |
| `middle_proxy_nat_ip` | `203.0.113.10`                 | Внешний IP, который нужно заменить на реальный |
| `log_level`           | `normal`                       | Базовый уровень логов Telemt                   |

### `[general.modes]`

Сейчас шаблон ориентирован на TLS-only deployment:

| Key       | Default |
|-----------|---------|
| `classic` | `false` |
| `secure`  | `false` |
| `tls`     | `true`  |

### `[general.links]`

| Key           | Default        | Meaning                               |
|---------------|----------------|---------------------------------------|
| `show`        | `*`            | Разрешает показывать startup links    |
| `public_host` | `203.0.113.10` | Внешний адрес для `tg://proxy` ссылки |
| `public_port` | `443`          | Порт, который увидит клиент           |

### `[server]` и `[server.api]`

| Key                    | Default        | Meaning                                                              |
|------------------------|----------------|----------------------------------------------------------------------|
| `port`                 | `443`          | Порт слушателя внутри Telemt                                         |
| `proxy_protocol`       | `false`        | Включать только если upstream router реально передает PROXY protocol |
| `metrics_whitelist`    | loopback only  | Ограничение доступа к метрикам                                       |
| `server.api.enabled`   | `true`         | Включает локальный Control API                                       |
| `server.api.listen`    | `0.0.0.0:9091` | Внутренний адрес API в контейнере                                    |
| `server.api.read_only` | `true`         | Оставляет API в read-only режиме                                     |

Практическое правило: Control API должен публиковаться только на loopback хоста, а `proxy_protocol` не надо включать
заранее без реального reverse-proxy сценария.

### `[[server.listeners]]`

| Key        | Default        | Meaning                                                |
|------------|----------------|--------------------------------------------------------|
| `ip`       | `0.0.0.0`      | Адрес прослушивания                                    |
| `announce` | `203.0.113.10` | Публичный адрес, который должен совпадать с внешним IP |

### `[censorship]`

| Key             | Default                    | Meaning                |
|-----------------|----------------------------|------------------------|
| `tls_domain`    | `www.google.com`           | FakeTLS-домен          |
| `mask`          | `true`                     | Включает masking path  |
| `mask_port`     | `443`                      | Порт fallback backend  |
| `tls_emulation` | `true`                     | Эмуляция TLS поведения |
| `tls_front_dir` | `/var/lib/telemt/tlsfront` | Каталог TLS front data |

Если хочешь более естественный production path, вместо чужого домена лучше использовать свой домен и свой fallback
backend. Практические детали смотри в [Reverse Proxy](reverse-proxy.md).

### `[access.users]`

В шаблоне создается пользователь:

```toml
[access.users]
"main" = "0123456789abcdef0123456789abcdef"
```

Здесь важно:

- имя пользователя влияет на startup link и API output
- secret должен быть реальным и уникальным
- при multi-user сценариях стоит отдельно проверять operational behavior и выдачу ссылок

## Что обычно меняют первым делом

Для большинства серверов достаточно сначала заменить:

- `middle_proxy_nat_ip`
- `public_host`
- `announce`
- `tls_domain`
- при необходимости `PORT` и `public_port`

## Что не стоит обещать настройками

- `ad_tag` нельзя считать универсальной функцией для всех providers
- calls нельзя документировать как гарантированно supported use case
- `proxy_protocol` не является безусловным улучшением без корректной схемы L4-routing

## See Also

- [Getting Started](getting-started.md) - как эти настройки участвуют в первой установке
- [Providers](providers.md) - ограничения Telemt относительно других provider paths
- [Reverse Proxy](reverse-proxy.md) - когда нужны `mask_port`, fallback backend и `proxy_protocol`
