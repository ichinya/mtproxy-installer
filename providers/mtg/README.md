# mtg provider

## Базовый upstream

- Engine: `https://github.com/9seconds/mtg`

## Текущий статус

Это planned alt-provider.

Он еще не реализован в installer, но заранее зафиксирован как важный путь для будущего selector-а.

## Почему он нам нужен

- это один из самых известных и распространенных MTProto/FakeTLS engines в старых и текущих инструкциях;
- он хорошо подходит для простого Docker deployment;
- он полезен как второй provider с другим operational profile.

## Что нужно реализовать позже

- provider-specific compose path;
- env/config template;
- генерацию FakeTLS secret;
- provider-specific docs по reverse proxy;
- test matrix по media/CDN и нестандартным портам.

## Что важно помнить

- `mtg v2` нельзя считать provider-ом с полной поддержкой `ad_tag`;
- behavior по media/CDN надо проверять отдельно;
- описывать его нужно как отдельный компромиссный путь, а не как drop-in replacement для `telemt`.
