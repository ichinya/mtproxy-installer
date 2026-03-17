[← Reverse Proxy](reverse-proxy.md) · [Back to README](../README.md)

# Troubleshooting

Этот документ собирает практические выводы из статьи на Habr, комментариев и upstream discussions.

Это не абсолютная истина, а рабочий operational memory проекта.

## Порт `443` против `8443` и `9443`

Практический вывод из обсуждений:

- `443` - лучший вариант по правдоподобию HTTPS-трафика;
- `8443` и `9443` часто работают как fallback;
- если порт не `443`, трафик выглядит менее естественно для внешнего наблюдателя.

Вывод для репозитория:

- `443` держим как default;
- `8443` и `9443` явно документируем как fallback, а не как равнозначные варианты.

## Reverse proxy и SNI routing

Наиболее жизнеспособный путь из комментариев:

- делить `443` на уровне L4;
- использовать `nginx stream` + `ssl_preread` или Traefik TCP + `HostSNI(...)`;
- отправлять только proxy-domain на backend MTProxy/Telemt;
- обычный TLS-трафик отдавать на реальный HTTPS backend.

Вывод для репозитория:

- examples и docs по `nginx stream` и Traefik должны быть первоклассными, а не факультативными заметками.

## `proxy_protocol`

Одна из самых частых точек ошибок.

Типичные симптомы:

- `broken header` в nginx;
- в логах backend виден только `127.0.0.1` или адрес docker bridge;
- одно плечо reverse proxy работает, а второе ломается.

Что важно:

- PROXY protocol должен включаться там, где реально есть клиентский IP для передачи;
- backend обязан уметь его принимать;
- для `telemt` это отдельная provider-specific возможность, а не побочный эффект reverse proxy.

Вывод для репозитория:

- docs должны отдельно описывать, где `proxy_protocol` «заворачивается» и где «разворачивается»;
- test matrix должна включать `proxy_protocol` on/off.

## Media/CDN и `dc_overrides`

По комментариям и upstream issue видно, что иногда наблюдается такой симптом:

- текстовые сообщения ходят;
- медиа или публичные каналы грузятся плохо или не грузятся совсем.

Практический workaround, который повторяется несколько раз:

```toml
[dc_overrides]
"203" = "91.105.192.100:443"
```

Почему это важно:

- `203` связан с CDN path;
- это не теоретическая настройка, а реальный operational workaround, который пользователи уже применяют.

Вывод для репозитория:

- issue про `dc_overrides` должен остаться отдельным и подробным;
- docs должны хотя бы объяснять, зачем этот блок может понадобиться;
- installer позже может получить optional flag или advanced toggle, но только после проверки.

## Пустой SNI на iOS

Из комментариев есть важный кейс:

- некоторые пользователи сталкивались с тем, что iOS-клиент Telegram отправлял пустой SNI;
- в итоге web part продолжала работать, а proxy backend отваливался;
- у одного из комментаторов это решилось обновлением Telegram-клиента.

Вывод для репозитория:

- troubleshooting должен включать проверку версии клиента;
- нельзя сразу считать такую проблему багом reverse proxy конфига.

## Свой домен против чужого SNI

В комментариях заметно желание использовать не чужой домен вроде `google.com`, а свой реальный домен и свой
fallback-сайт.

Практический смысл:

- конфигурация выглядит естественнее;
- меньше ощущения «поддельного SNI ради маскировки»;
- проще объяснить схему администрирования.

Вывод для репозитория:

- docs должны поддерживать оба сценария, но own-domain + own-fallback выглядит как более аккуратный production path.

## Calls

Звонки нельзя документировать как supported feature MTProxy.

По комментариям и upstream-логике:

- calls часто не работают или работают нестабильно;
- это ограничение скорее на уровне самого класса решений, а не просто баг конкретного installer-а.

Практическая интерпретация для этого репозитория:

- даже при корректном `tg://proxy`, рабочем `v1/health` и нормальном текстовом трафике calls могут не подниматься;
- warning-строки вида `Upstream failed after retries: Connection timeout to ...:8888` и `ME immediate refill failed ...`
  показывают деградацию Middle Proxy path, но не являются доказательством, что именно installer "сломал звонки";
- считать багом installer-а стоит случаи, где ломаются сообщения, каналы, media или сам Control API, а не только calls.

Вывод для репозитория:

- installer не должен обещать рабочие calls;
- docs должны четко отделять «доступ к Telegram и медиа» от «голосовых звонков».

## VPS в РФ против зарубежного VPS

Статья подает тезис, что VPS внутри РФ может быть выгоднее.

Комментарии показывают, что реальная картина неоднородная:

- многое зависит от провайдера, региона и конкретного маршрута;
- есть кейсы, где РФ VPS удобнее;
- есть кейсы, где зарубежный VPS дает более стабильный результат.

Вывод для репозитория:

- не формулировать это как универсальное правило;
- писать честно: «проверять по месту и по своему провайдеру».

## Runtime логи: что нормально, что требует внимания

### Быстрая диагностика: Direct DC vs Middle Proxy

Проверьте логи после старта:

```bash
docker compose logs telemt 2>&1 | grep -E "(Transport|STUN-Quorum|Middle Proxy)"
```

**Хорошо (Middle Proxy включен):**
```
INFO telemt::network::probe: STUN-Quorum reached, IP: YOUR_IP
INFO telemt::maestro: Transport: Middle-End Proxy - all DC-over-RPC
```

**Плохо (Direct DC — возможны блокировки):**
```
WARN telemt::maestro: No usable IP family for Middle Proxy detected; falling back to direct DC
INFO telemt::maestro: Transport: Direct DC - TCP - standard DC-over-TCP
```

**Исправление:**
```bash
# Включить STUN probe
sed -i 's/middle_proxy_nat_probe = false/middle_proxy_nat_probe = true/' /opt/mtproxy-installer/providers/telemt/telemt.toml
docker compose restart
```

### Нормальные сообщения при старте

```
INFO telemt::maestro: Telemt MTProxy v3.3.15
INFO telemt::maestro: Modes: classic=false secure=false tls=true
INFO telemt::maestro: TLS domain: www.wikipedia.org
INFO telemt::maestro: Mask: true -> www.wikipedia.org:443
INFO telemt::api: API endpoint: http://0.0.0.0:9091/v1/*
INFO telemt::maestro::listeners: Listening on 0.0.0.0:443
INFO telemt::links: --- Proxy Links (YOUR_IP) ---
```

Это означает: контейнер стартовал, TLS-маскировка включена, API доступен.

### "No usable IP family for Middle Proxy"

```
WARN telemt::maestro: No usable IP family for Middle Proxy detected; falling back to direct DC
INFO telemt::maestro: Transport: Direct DC - TCP - standard DC-over-TCP
```

Это означает, что Middle Proxy не может быть использован. Решение:

1. Установите `middle_proxy_nat_probe = true` в конфигурации
2. Перезапустите контейнер

После успешного определения IP через STUN вы увидите:

```
INFO telemt::network::probe: STUN-Quorum reached, IP: YOUR_IP
INFO telemt::maestro: Transport: Middle-End Proxy - all DC-over-RPC
```

Middle Proxy маршрутизирует трафик через сервера Telegram (порт 8888) вместо прямого подключения к DC (порт 443),
что обходит блокировки на уровне TLS.

### Connectivity check

```
INFO telemt::maestro::connectivity: ================= Telegram DC Connectivity =================
INFO telemt::maestro::connectivity:   IPv4 only / IPv6 unavailable
INFO telemt::maestro::connectivity:     DC1 [IPv4] 149.154.175.50:443                            126 ms
INFO telemt::maestro::connectivity:     DC2 [IPv4] 149.154.167.51:443                            32 ms
...
```

Если все DC показывают задержки — connectivity OK. Если какой-то DC недоступен — возможно, проблема с маршрутизацией.

### Сканеры портов

```
INFO telemt::maestro::listeners: Connection closed during initial handshake peer=147.45.169.173:51706 error=IO error: expected 64 bytes, got 0
```

Это сканеры портов или боты, которые подключаются к 443 и отправляют HTTP/нечитаемые данные. Telemt корректно разрывает
такие соединения. Это нормально для публичного порта 443.

### Реальные подключения

```
WARN telemt::proxy::relay: Activity timeout user=public c2s_bytes=5268 s2c_bytes=720719 idle_secs=1804
```

Это реальный клиентский трафик:

- `c2s_bytes` — client to server (клиент → Telegram)
- `s2c_bytes` — server to client (Telegram → клиент)
- `idle_secs` — время неактивности до timeout

Если `s2c_bytes >> c2s_bytes` — типичная картина для скачивания медиа/сообщений.

### Timeout ошибки

```
WARN telemt::maestro::listeners: Connection closed with error peer=37.76.153.5:1190 error=IO error: Operation timed out (os error 110)
```

Сетевой timeout между клиентом и сервером. Возможные причины:

- проблемы на стороне клиента (мобильный интернет)
- временные проблемы с маршрутизацией
- блокировка на уровне провайдера

Если такие ошибки редки — не требуют действия. Если массовы — проверьте сеть/фильтрацию.

### DC Connection Timeout (блокировка на уровне TLS)

```
WARN telemt::transport::upstream: Upstream failed after retries: Connection timeout to 149.154.167.51:443
WARN telemt::maestro::listeners: Connection closed with error error=Connection timeout to 149.154.167.51:443
```

Симптом: TCP-соединение устанавливается, но TLS-handshake зависает. Это признак блокировки на уровне TLS
со стороны Telegram для вашего IP.

**Решения:**

1. **Включить Middle Proxy** (рекомендуется):
   ```toml
   middle_proxy_nat_probe = true
   ```

2. **Отключить TLS эмуляцию**:
   ```toml
   tls_emulation = false
   ```

3. **Сменить TLS-домен** на другой популярный:
   ```toml
   tls_domain = "www.wikipedia.org"
   ```

После включения Middle Proxy трафик пойдет через `port 8888` (RPC) вместо `port 443` (TLS), что обходит блокировку.

## Операционные команды

### Проверка здоровья API

```bash
curl http://127.0.0.1:9091/v1/health
```

Ожидаемый ответ:

```json
{
  "ok": true,
  "data": {
    "status": "ok",
    "read_only": true
  },
  "revision": "..."
}
```

### Получение proxy links

```bash
curl http://127.0.0.1:9091/v1/users
```

### Просмотр логов

```bash
docker compose -f /opt/mtproxy-installer/docker-compose.yml \
  --project-directory /opt/mtproxy-installer \
  --env-file /opt/mtproxy-installer/.env logs -f telemt
```

### Перезапуск

```bash
docker compose -f /opt/mtproxy-installer/docker-compose.yml \
  --project-directory /opt/mtproxy-installer \
  --env-file /opt/mtproxy-installer/.env restart
```

### Остановка

```bash
docker compose -f /opt/mtproxy-installer/docker-compose.yml \
  --project-directory /opt/mtproxy-installer \
  --env-file /opt/mtproxy-installer/.env down
```

## Обновление

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/update.sh | sudo bash
```

См. [update.sh](../update.sh) — сохраняет секрет и конфиг, обновляет образ и compose.

## Удаление

```bash
curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo bash
```

См. [uninstall.sh](../uninstall.sh) — удаляет контейнер, образ и данные.

## See Also

- [Getting Started](getting-started.md) - базовая проверка после установки
- [Configuration](configuration.md) - параметры, которые чаще всего приходится корректировать
- [Reverse Proxy](reverse-proxy.md) - схемы, где часто проявляются сетевые проблемы
