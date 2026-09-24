# Визуализация будущего ingestion-контура

## Цель

Расширить лабораторную схему за текущим Sender и показать будущий путь batch-а без подмены его уже реализованной частью:

```text
Reader(s) → Throttler → Sender → HTTP receiver (Go) → S3/Vaultbox → Postgres outbox → Kafka
```

Основная магистраль остаётся на одной горизонтальной строке. Внутренности Target раскрываются вниз как вложенный ingestion-контур, а не переносят весь pipeline на второй ряд.

## Границы первого визуального этапа

- Это будущая статическая схема, а не новый backend-контракт и не обещание работающей телеметрии.
- Не добавлять фиктивные очереди, worker pools, прогресс или анимацию до появления соответствующей runtime-модели.
- Не использовать сторонние брендовые логотипы; продолжать существующий приборный язык блоков, ports и каналов.
- Существующий `Target` постепенно заменяется составным блоком **INGESTION SERVICE**, а не дублируется вторым независимым Target.

## Топология

### HTTP receiver

Первый внутренний блок Target — Go HTTP receiver. Его вход связан с Sender существующим HTTP-каналом; рядом допускается короткая подпись `POST batch`.

Receiver принимает batch и ждёт подтверждения следующего шага. Это не самостоятельная очередь и не отдельный worker pool.

### S3 / Vaultbox

Следующий блок — объектное хранилище raw batch-ей. На первом этапе handler ждёт успешного `PutObject` до ответа Sender; отдельной основной очереди перед S3 нет.

Показывать только фактически осмысленные будущие показатели:

- `Objects written/s`;
- `Data written MB/s`;
- `In-flight uploads` как агрегат HTTP handlers, ожидающих `PutObject`;
- retries и terminal failures.

Не показывать «заполненность S3» как health progress bar: общий объём/число объектов — нейтральная телеметрия, а зелёный/жёлтый/красный должны означать риск по latency, retry или ошибкам записи.

### Postgres outbox

После успешного object write сервис фиксирует запись outbox в Postgres. Здесь допустимо показывать:

- размер неопубликованного outbox;
- возраст самой старой записи;
- скорость записи и публикации;
- failures.

Когда появится operational limit, полоска заполнения относится именно к outbox backlog, а не к объёму базы данных.

### Kafka

Kafka следует за outbox. Визуально это широкая шина с partition-ячейками, а не ещё один одиночный прибор. Заливка partition означает lag, а не физическую ёмкость Kafka.

Показатели:

- `Produce`;
- число partitions;
- общий lag;
- возраст oldest lag;
- retry/errors.

Цвет Kafka определяется худшей partition, а не средним lag: одна проблемная partition должна оставаться видимой.

## Цвета и состояние

Зелёный, жёлтый и красный показывают здоровье обработки: успешную работу, рост задержки/retry и terminal failure соответственно. Они не обозначают общий объём S3, Postgres или Kafka.

Постоянные объёмные показатели — число объектов, MB, outbox events и lag — выводятся нейтральным текстом. Любая цветная полоска должна иметь конкретный operational denominator, зафиксированный runtime-моделью.

## Будущий uploader pool

Когда появится пул S3 uploader workers, его topology будет отдельной:

```text
HTTP handler → bounded dispatch channel → uploader pool → S3
                         ↑
                 handler ждёт результат
```

Этот bounded channel — диспетчер параллелизма, а не durable main queue. HTTP success подтверждается только после успешного `PutObject`; после restart неподтверждённый Sender batch повторяется.

Только после такой реализации схема получает worker slots (`idle`, `uploading`, `retrying`, `failed`), desired/live/busy и глубину dispatch channel.

## Последовательность реализации

1. Сделать статический composition Target/INGESTION SERVICE и топологию HTTP → S3 → Postgres outbox → Kafka.
2. Согласовать реальные domain-модели и telemetry для receiver, S3, outbox и Kafka.
3. Подключать к схеме только опубликованные runtime snapshots и controls.
4. После появления uploader pool добавить его slots, bounded dispatch channel и соответствующие индикаторы.
5. Ввести адаптивный layout: на широком canvas магистраль горизонтальна; на узком экране Target раскрывается вниз без переноса основной магистрали.

## Проверки перед реализацией

- Проверить реальный порядок durability: S3 object, запись outbox и публикация Kafka.
- Зафиксировать, что именно означает успешный HTTP response Sender.
- Для каждого визуального индикатора определить источник snapshot, единицу измерения, denominator и условия цвета.
- Не добавлять field, control или animation, пока их нельзя получить из реального runtime state.
