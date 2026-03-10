# Providers

Эта директория нужна не только для текущих файлов, но и как точка сборки всей будущей provider-oriented архитектуры.

## Текущее состояние

- `providers/telemt` - текущий default path
- `providers/mtg` - placeholder для будущего alt-provider
- `providers/official` - placeholder для official/reference path

## Правило

Каждый provider должен со временем получить:

- `README.md` с upstream repo и caveats;
- example env/config;
- compose или другой reproducible launch path;
- заметки по `ad_tag`, reverse proxy, metrics, secret format и operational limits.
