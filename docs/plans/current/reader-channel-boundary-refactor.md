# Reader channel boundary refactor

## Цель

Сделать явной границу между runtime-состоянием Reader worker и telemetry
очереди Reader → Throttler. `channelTelemetry` наблюдает отправку и
backpressure, но не исполняет callbacks, меняющие состояние worker.

## Наблюдаемая проблема

`readerPool.sendBatch` передаёт в `channelTelemetry.sendWithBlocked` два
замыкания. Они меняют `readerWorker.blocked`, запускают политику forced
draining и обновляют Reader worker telemetry.

Из-за этого component, который измеряет channel, управляет переходами
состояния Reader worker. Переходы `reading` → `blocked` → `reading` не видны
целиком в `readerPool`.

## Целевое устройство

- Reader pool владеет состоянием worker и его переходами при отправке batch.
- Channel telemetry фиксирует факт отправки, blocked sender и длительность
  backpressure, но не вызывает код Reader pool обратно.
- Способ отправки остаётся cancellation-aware.
- Sender не получает искусственно одинаковый протокол, если его поток работы
  не требует такого перехода состояния.

## План работы

1. Зафиксировать три сценария отправки Reader batch: немедленная отправка,
   ожидание в заполненной очереди с последующей отправкой и отмена во время
   ожидания.
2. Выбрать границу API, в которой Reader pool сам выполняет переходы worker
   `reading`/`blocked`, а channel telemetry получает только наблюдаемые факты.
3. Убрать callbacks из `channelTelemetry` API и перенести владение переходами
   Reader worker в Reader pool.
4. Проверить сохранение telemetry очереди: sent totals, blocked senders,
   blocked duration и корректное завершение ожидания при отмене.
5. Проверить Reader telemetry в трёх сценариях: worker остаётся `reading` при
   немедленной отправке, становится `blocked` только на время ожидания и не
   возвращается в ошибочное активное состояние после отмены.

## Готовность

- `channelTelemetry` не принимает и не исполняет callbacks, меняющие runtime
  Reader worker.
- Все переходы `readerWorker.blocked` находятся в Reader pool.
- Наблюдаемая семантика channel и Reader telemetry сохранена для трёх
  сценариев отправки.
