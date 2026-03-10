# official provider

## Базовый upstream

- Engine: `https://github.com/TelegramMessenger/MTProxy`

## Текущий статус

Это reference / optional future provider.

Сейчас он не является default path и не должен смешиваться с текущей реализацией на `telemt`.

## Зачем он нам вообще нужен

- это canonical reference по official MTProxy behavior;
- он полезен для аккуратной документации по `@MTProxybot`, promoted channels и middle-proxy semantics;
- он позволяет не строить документацию по `ad_tag` только на слухах и комментариях.

## Почему он не default

- installer UX и Docker path у `telemt` сейчас лучше совпадают с нашими целями;
- нам важнее сначала стабилизировать provider-specific docs и install path для `telemt`.

## Что можно реализовать позже

- reference provider profile;
- docs по official registration flow;
- optional path для тех, кому нужен именно official stack.
