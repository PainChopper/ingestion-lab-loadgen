# Агрегирование состояния Reader workers

## Контекст

Текущий UI показывает отдельные Reader workers как стабильные сущности: `workerId`,
activity, lifecycle и source. Это создаёт «панель жизни горутин», хотя пользователю
не нужно знать, чем занимается конкретный worker №N.

Внутренний lifecycle Reader pool всё ещё нужен для корректного downscale: `busy`,
`draining` и `blocked` участвуют в выборе worker и штатном завершении работы.
Draining Reader, остановившийся на заполненном канале, ждёт освобождения очереди;
принудительная отмена и replay файла для этого пути не применяются.

## Решение

Убрать индивидуальную наблюдаемость Reader workers и перейти к агрегированному
представлению.

UI рисует анонимные Reader processors по количеству workers и их агрегированным
состояниям. Нарисованный processor не имеет стабильной идентичности, source,
hover/inspector или связи с конкретной Go-горутиной.

## Целевое UI-представление

Показывать:

- общее число live Reader workers;
- сколько читает;
- сколько простаивает;
- сколько ждёт свободное место в очереди;
- сколько находится в draining.

Для визуализации использовать анонимные processor-маркеры, разложенные сеткой
или группами. При большом числе workers ограничить число отдельных маркеров и
показывать компактный множитель, например `×24`.

`draining` — модификатор lifecycle, а не обязательно отдельная activity-категория.
Базовые activity должны быть взаимоисключающими:

- `idle = live - busy`;
- `blocked = количество worker.blocked`;
- `reading = busy - blocked`.

`draining` может накладываться на любую из этих activity и отображаться отдельным
стилем или счётчиком.

## Что удалить из публичной наблюдаемости

- `readerWorkerSlot`;
- `workerSlots` из Reader snapshot;
- `workerId` из HTTP snapshot и UI-модели;
- per-worker `source`;
- activity `completed`: это короткая переходная фаза между EOF и `idle`/выходом
  worker и не имеет продуктовой ценности;
- map slot'ов и per-worker setters в `readerTelemetry`, если после миграции они
  больше не нужны.

`workerID` допустимо оставить только внутренним техническим идентификатором
Reader pool — для map, логов, source error и детерминизма. Он не должен быть
публичным контрактом.

## Владение состоянием

`readerTelemetry` не должен хранить вторую копию lifecycle worker'ов.

Агрегированные counts следует получать из `readerPool` под `readerPool.mu`, где
находится authoritative state: `workers`, `busy`, `blocked`, `draining`, `forced`.

В `readerTelemetry` оставить только потоковые факты:

- `rowsRead`;
- read TPS;
- при необходимости агрегированный source или ошибку, если их смысл для
  нескольких параллельных workers чётко определён.

## Channel telemetry

Наблюдаемость очереди Reader → Throttler сохранить.

В основном UI достаточно:

- `depth / capacity`;
- визуального давления или backpressure;
- понятного статуса «Reader ждёт очередь».

Подробности вроде `blockedMs`, `oldestBlockedSenderMs` и точных totals допустимо
оставить в diagnostics, но не делать центральным UI-сюжетом.

## Не менять в этом этапе

- safe downscale;
- `busy`, `draining` и `blocked` как внутренние поля;
- штатное ожидание draining Reader освобождения заполненного канала;
- callback-free границу Reader pool / channel telemetry, реализованную в T0386.

Отдельно рассмотреть позднее: `blockedChannelSend` сейчас хранит channel и batch,
поэтому telemetry фактически участвует в доставке. Это можно упростить отдельным
рефакторингом, но не смешивать с UI-агрегацией.

## Критерии приёмки

- UI не отображает идентичность или source конкретного Reader worker.
- В публичном snapshot нет per-worker slots и `workerId`.
- UI получает только агрегированные counts.
- `readerPool` остаётся единственным источником lifecycle worker'ов.
- Нет второй slot-модели в telemetry.
- `completed` отсутствует как отображаемое worker-состояние.
- Draining Reader на заполненном канале штатно ждёт освобождения очереди.
- Channel occupancy и backpressure остаются наблюдаемыми.
