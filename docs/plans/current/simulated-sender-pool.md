# Simulated Sender pool — план реализации

## Назначение и границы

Первый Sender-срез заменяет текущий `blackHole` внутренним simulated transport. Каждый принятый batch завершается ровно через фиксированные 10 ms; внешний HTTP server, HTTP-клиент, сеть и target не создаются. Над существующим Sender channel запускается fixed pool из `N` Sender workers.

В срез входят private transport, fixed pool, lifecycle Sender, private telemetry, значение существующего `sender.workers` и deterministic Go tests. Reader workers, Producer, Throttler ownership, channel-capacity policy и публичные команды сохраняются.

Вне среза: внешний или реальный HTTP, endpoint, retries, backoff, target errors, dynamic resize, новые config/policy/command поля, новые public snapshot/Prometheus fields и frontend.

## Принятые решения

- Обрабатывается целый `[]Transaction` batch, а не отдельные transaction.
- `simulatedTransport` ждёт `10 * time.Millisecond` для каждого batch и успешно завершает его; transport не знает о каналах, lifecycle или telemetry.
- Throttler остаётся единственным writer Sender channel, event loop — владельцем lifecycle, закрытия и очистки каналов.
- Sender telemetry защищается собственным private `sync.Mutex`; snapshot возвращает value-copy, а lifecycle primitives не помещаются под этот mutex.
- Pool использует отдельный context для прекращения будущего intake и `sync.WaitGroup`/`done` для подтверждения завершения всех workers.
- Pause прекращает только будущие receives. Уже принятый batch завершается, после чего подтверждается Pause; очереди Sender channel и batch, удерживаемый Throttler, сохраняются до Resume.
- Reset из paused после joins Producer/Throttler дренирует queued/held batches и очищает telemetry/progress. Reset из running сохраняет существующий conflict.

Единственный незакрытый выбор — положительное фиксированное число workers `N`. В текущих policy, config и control API нет его источника или default, а frontend simulation не является backend-контрактом. После решения владельца `N` вводится как private fixed construction constant. Config key, UI control и resize для него не добавляются.

## Архитектура и lifecycle

```text
Producer -> Reader channel -> Throttler -> Sender channel -> Sender pool (N workers)
                                                          -> simulated transport (10 ms/batch)
```

`startSenderPool` получает read-only Sender channel, фиксированный `N`, intake context, telemetry и transport; он создаёт ровно `N` workers и возвращает cancel/done boundary. Worker перед receive проверяет intake context, после успешного receive сразу записывает admission через существующий `senderChannel.recordReceive`, затем фиксирует accepted и in-flight, выполняет transport без отмены intake и записывает terminal completion. Перед следующим receive context проверяется снова. Workers не закрывают и не дренируют каналы и не отправляют command acknowledgements.

`consumer.go`, `startConsumer`, `consumeBatches*`, `atomic consumedSinceTick`, `consumeTransaction` и глобальный `blackHole` удаляются либо заменяются узкими Sender-pool equivalents; старый consumer и новый pool одновременно не сохраняются.

### Run из idle

Сохраняются текущая подготовка/reuse Reader и Sender channels, Producer и Throttler запускаются как прежде. После перехода в `running` создаётся один pool из `N` workers. Повторный Run в `running` не создаёт второй pool, Producer или Throttler.

### Pause и Resume

1. Отменить только intake context Sender pool.
2. Дождаться `done`; уже полученный batch должен закончить 10 ms transport.
3. Собрать completed delta, зафиксировать elapsed/progress, перевести lifecycle в paused и дождаться существующего Throttler acknowledgement.
4. При Resume снять Throttler pause и создать fresh pool `N` над тем же открытым Sender channel.

До acknowledgement новый Sender receive невозможен; уже завершённые batch не отправляются повторно.

### Reset и teardown

Reset из paused начинается после join pool, затем отменяет и ждёт Producer/Throttler, дренирует каналы и очищает Reader, channel, Sender telemetry и progress. Deferred shutdown выполняет тот же порядок: cancel/join pool, cancel/join Producer и Throttler, затем close/drain/detach channels и telemetry. Это исключает `send on closed channel` и stale worker mutation после reset/teardown.

## Telemetry и snapshot

Private `senderTelemetry` под одним mutex хранит live workers, in-flight batches/transactions, accepted batches/transactions, completed batches/transactions и completed transactions since last metrics tick. Mutex не удерживается во время transport, ожидания `done` или command acknowledgement.

`sampleCompletedTransactions()` вызывается event loop на metrics tick и заменяет текущий atomic progress для `Run.TotalTransactions`, `transactionsTotal` и `actualTPS`; progress означает terminally completed transactions, а не admission в channel. Существующее поле `/api/loadgen/snapshot` `sender.workers` сохраняется: оно равно числу живых workers (`N` в running и `0` после joined Pause/Reset/teardown). JSON schema, Prometheus names и frontend не меняются.

## Реализация и проверки

Изменение выполняется одним связанным срезом: private Sender-pool/transport types, event-loop cancel/done seam, telemetry reset/detach ordering, удаление black-hole path, actual `sender.workers` и тесты меняются вместе.

Для lifecycle-тестов private fake transport использует `entered`, `release`, `completed`; assertions не зависят от `time.Sleep`, timeout применяется только как защита от deadlock. Проверяются:

- старт ровно `N` workers и отсутствие второго pool при повторном Run;
- exactly-once передача каждого целого unique batch без проверки fairness или global completion FIFO;
- production constant 10 ms, а lifecycle — через fake transport;
- Pause acknowledgement только после release и terminal completion принятого batch, затем отсутствие нового receive;
- Resume queued/Throttler-held batches без повторной отправки;
- Reset/teardown join pool до writer cancel, drain, close/detach и очистки telemetry;
- раздельность admission/completion, согласованность `liveWorkers`/`inFlight`, `sender.workers == N` в running и `0` после joined stop.

После реализации запускаются focused tests и `go test -race ./...` штатным entrypoint проекта.
