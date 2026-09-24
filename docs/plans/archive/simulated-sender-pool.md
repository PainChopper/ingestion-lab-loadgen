# Reader(s), Sender pool и HTTP delivery

## Итог

Sender pipeline получает управляемый pool: configuration policy задаёт desired state, а pool постепенно приводит к нему фактическое число workers. Целевая схема: `Reader(s) → Reader channel → Throttler → Sender channel → Sender pool → HTTP endpoint`.

Sender выполняет HTTP POST по startup-only адресу `sender.api.url` (в локальной конфигурации — `/internal/test/ingest`). Ответы endpoint классифицируются как успешные, retryable или terminal; retry policy применяется к retryable HTTP/network/timeout исходам.

## Конфигурация и snapshot

Актуальные Go-символы, тесты и current-документация переименовываются из `Producer` в `Reader`; на схеме используется `Reader(s)`. Архивные документы не переписываются.

В `config.toml` добавляются strict policy sections:

```toml
[sender.workers]
default = 32
min = 1
max = 32
step = 1
unit = "workers"
mutability = "immediate"

[sender.retry]
max_attempts = 3
backoff_base_ms = 250
backoff_multiplier = 2
jitter_percent = 20
mutability = "startup-only"
```

Policy snapshot публикует workers и retry policy. Команда сразу изменяет workers; retry policy только читается из configuration.

Строгие команды используют существующее тело `{ "action": string, "value": number }`:

- `set-sender-workers`: целое `1…32` с шагом `1`.

Каждая допускается в `idle`, `running` и `paused`; неизвестный ключ, trailing JSON, `null`, дробное, строковое, вне диапазона или вне шага значение дают `400`.

В snapshot:

- `sender.workers` — desired value из configuration policy;
- `sender.liveWorkers` — фактическое число работающих goroutine;
- `sender.drainingWorkers` — workers, завершающие выход из pool.

В `idle` и `paused` desired value сохраняется, а `liveWorkers` и `drainingWorkers` равны нулю.

`sender` имеет ровно четыре поля: `workers`, `liveWorkers`, `drainingWorkers`, `workerSlots`. У каждого slot ровно `id`, `ordinal`, `activity`, `lifecycle`, `terminalError`; slots упорядочены по `ordinal`, а `id` имеет вид `sender-worker-<ordinal>`. `workerSlots.length` равен `liveWorkers`, число draining slots равно `drainingWorkers`.

## Приведение Sender pool к заданному размеру и lifecycle

При изменении Sender workers command обновляет desired state и запускает приведение pool к заданному размеру:

- scale up сразу создаёт недостающие workers;
- scale down помечает workers с наибольшими ordinal как `draining`;
- draining worker не принимает новый batch, но завершает уже принятый batch, включая retry, после чего выходит;
- повторное scale up снимает draining-метку с ещё живых workers прежде, чем создавать новые;
- accepted batch и очереди не теряются.

`Pause` прекращает новые receives и ждёт завершения всех accepted batch. `Reset` и teardown сначала join-ят Sender pool, затем останавливают Reader(s) и Throttler, после чего очищают очереди.

Retryable HTTP/network/timeout исход запускает до трёх попыток с backoff 250 и 500 ms и jitter ±20%. После третьей неудачи batch получает terminal failure. Terminal error остаётся на worker до следующего успешного batch.

## UI и цвета

Существующие `Target delay` и `Target error rate` переименовываются и становятся Sender controls.

Каждый worker slot публикует activity (`idle`, `in-flight`, `backoff`), lifecycle (`active`, `draining`) и `terminalError`. Цвет выбирается по приоритету:

| Состояние | Цвет | Значение |
|---|---|---|
| terminal error | красный `#ff6748` | Ошибка окончательная; держится до следующего успеха |
| retry/backoff | оранжевый `#ff9f43` | Ошибка временная, ожидается повтор |
| draining | фиолетовый `#c49cf5` | Worker плавно выходит из pool |
| in-flight | зелёный `#79d957` | Обрабатывается batch |
| idle | приглушённый | Worker ждёт batch |

Добавляется CSS token `--orange`; жёлтый не используется для Sender retry или drain. UI одновременно показывает desired, live и draining count.

## Проверки

- Go: strict config decode, команды и границы; reconciliation up/down/up; draining worker не принимает новые batch; accepted batch/retry не теряются; Pause/Reset/teardown; exactly-once handoff; retry и sticky terminal error; `go test ./...` и `go test -race ./...`.
- HTTP: strict policy/snapshot, desired/live/draining values, worker slots и команды.
- Frontend: strict decoder, migrated controls, фиолетовый draining, оранжевый retry, красный terminal error и сброс красного после success.
- Current docs используют `Reader(s)`; архивные документы остаются без изменений.

## Обязательное итоговое review после реализации

После завершения текущей реализации и исправлений review запускается отдельное жёсткое read-only review всего накопленного diff от последней принятой точки. До завершения кодерского этапа его не запускать.

Review проверяет не только работоспособность, но и отсутствие лишних или мёртвых хвостов:

- старые `Producer` и `consumer` abstractions, aliases, тестовые helpers, labels и current-документация;
- целостность пути `config → Go → HTTP → frontend`: policy, commands, applied snapshot, strict decoder, controls и presentation;
- lifecycle, ownership каналов и goroutines, cancellation, reconciliation, Pause/Reset/teardown и race risks;
- тесты: дубли без новой ценности, бессмысленные fixtures, непокрытые инварианты и недостающие межоперационные сценарии.

Итоговое review не запускает tests, build или стенд и не меняет product. При замечаниях требуется отдельный узкий follow-up и повторная проверка до commit/push.
