# CLI LoadGen

## Контекст

LoadGen запускает один прогон на процесс и обслуживает HTTP API для наблюдения
и изменения поддерживаемых настроек. CLI позволяет запустить сервис и работать
с уже запущенным экземпляром.

## Цель

Поддерживаемые команды:

```text
loadgen serve [--config FILE]
loadgen status [--url URL]
loadgen set reader-workers N [--url URL]
loadgen set sender-workers N [--url URL]
loadgen set requested-tps N [--url URL]
loadgen set throttler-installed <true|false> [--url URL]
```

## Решение

Это два способа запуска одного бинарника.

### Локальный service

```text
loadgen serve [--config FILE]
```

- без аргументов бинарник запускает сервис с `config.toml` из текущего
  каталога;
- `serve` делает то же самое явно;
- `--config FILE` выбирает другой TOML-файл;
- прогон начинается автоматически после успешной загрузки конфигурации.

### Remote-admin client

```text
loadgen status   [--url URL]
loadgen set reader-workers N [--url URL]
loadgen set sender-workers N [--url URL]
loadgen set requested-tps N [--url URL]
loadgen set throttler-installed <true|false> [--url URL]
```

- `status` читает `/api/loadgen/snapshot`; JSON также доступен напрямую через
  этот endpoint;
- `set` отправляет одну из четырёх поддерживаемых настроек через
  `/api/loadgen/commands` и проверяет результат следующим снапшотом;
- `--url` по умолчанию равен `http://127.0.0.1:8080` и валидируется до
  сетевого запроса.

## Формат вывода

- успешный `set` печатает краткий итог: настройку, значение и состояние;
- `status` печатает читаемое состояние; JSON доступен через HTTP endpoint
  `/api/loadgen/snapshot`;
- ошибки сети, HTTP error response и malformed snapshot печатаются в stderr с
  ненулевым exit code;
- CLI не печатает retry policy, секреты, request body или скрытые config values.

## Поведение

- Обычный запуск и `serve` используют `config.toml` текущего каталога;
  `serve --config FILE` использует указанный файл.
- Неизвестная CLI-команда получает общий отказ с кодом usage; неизвестное
  HTTP-действие получает `400 Bad Request`.
- `status` показывает читаемое состояние; JSON snapshot доступен по HTTP.
- `set` принимает только `reader-workers`, `sender-workers`, `requested-tps` и
  `throttler-installed`; команды используют HTTP validation.
- Сетевые/HTTP/validation ошибки имеют понятный stderr и ненулевой exit code.
