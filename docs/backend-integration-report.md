# Отчёт об интеграции frontend и backend

Дата анализа: 2026-08-09.

## Резюме

Текущий React frontend полностью работает через `SimulationAdapter`; реализации `HttpAdapter` нет ([frontend/src/main.tsx](../frontend/src/main.tsx#L5)). Go backend обслуживает старый каталог `./ui`, legacy endpoints `GET /status` и `POST /control`, которые несовместимы с новым контрактом ([cmdgen.go](../cmdgen.go#L31), [cmdgen.go](../cmdgen.go#L81), [cmdgen.go](../cmdgen.go#L94)).

Главный backend-факт: `targetTPS` хранится и меняется, но не влияет на фактический поток. `produceBatches` подключён напрямую к `consumeBatches`; метод `transactionThrottler.Throttle` нигде не вызывается ([main.go](../main.go#L28), [main.go](../main.go#L31), [main.go](../main.go#L47)). Поэтому текущее значение нельзя честно обозначать как реально применённый предел до включения throttler в pipeline.

Первый backend snapshot можно построить немедленно из `totalTransactions`, фиксированной конфигурации reader и сохранённого requested TPS, пометив остальные controls как `unavailable`, а неизвестные измерения как `null`. Legacy `actualTPS` не имеет семантически точного поля в новом wire snapshot: он измеряется после обработки транзакций black-hole consumer-ом, а не на границе reader, throttler, sender или HTTP.

## 1. Что UI уже умеет отображать

Верхняя панель отображает run state, Run/Pause, Reset, elapsed time, total transactions и requested TPS ([frontend/src/components/LabShell.tsx](../frontend/src/components/LabShell.tsx#L56)). Pipeline показывает reader и sender workers, reader/read TPS, throttler requested/admitted TPS, две очереди, HTTP link и target ([frontend/src/components/pipeline/PipelineSvg.tsx](../frontend/src/components/pipeline/PipelineSvg.tsx#L44)). Inspector показывает подробные rates, counters, latency, blocked state и controls выбранного объекта ([frontend/src/components/inspectorViewModel.ts](../frontend/src/components/inspectorViewModel.ts#L91)).

Очередь использует разные поля для разных визуальных задач:

- форма кабеля зависит от `capacity` и `depthBatches`;
- цвет зависит от `flowState`;
- плотность движущихся маркеров зависит от `throughputTps`;
- активность marker lifecycle определяется также batch rates, handoff и монотонными enqueue/dequeue counters ([frontend/src/components/pipeline/usePipelineMarkerLifecycle.ts](../frontend/src/components/pipeline/usePipelineMarkerLifecycle.ts#L39));
- capacity, pending/preview, throughput, depth и blocked sender выводятся непосредственно рядом с кабелем ([frontend/src/components/pipeline/QueueCable.tsx](../frontend/src/components/pipeline/QueueCable.tsx#L300)).

## 2. Практическая таблица wire-полей и Go-владельцев

В колонке `wire field` используются имена проектного wire-контракта, а не текущие имена TypeScript snapshot. Этапы `V0`-`V6` определены ниже.

| UI element | wire field | Go source or owner | status now | implementation stage |
|---|---|---|---|---|
| Adapter badge | Не является backend wire-полем | Frontend выбирает adapter | Сейчас всегда `simulation` | V1 |
| Run badge, Run/Pause/Reset | `run.state` | Будущий lifecycle owner в control loop | Lifecycle отсутствует; legacy pipeline стартует при запуске процесса | V3 |
| Elapsed | `run.elapsedMs` | Будущий active run clock | Активное время без пауз не измеряется | V3 |
| Total | `run.totalTransactions` | `generatorState.totalTransactions`, обновляемый metrics tick ([main.go](../main.go#L20), [main.go](../main.go#L74)) | Можно отдать немедленно; legacy JSON использует строку | V1 |
| Requested TPS | `throttler.requestedTps` | `transactionThrottler.GetTPS` ([transaction_throttler.go](../transaction_throttler.go#L43)) | Значение доступно, но пока не ограничивает pipeline | V1 display, V2 control |
| Reader workers | `reader.workers` | Одна producer goroutine ([transaction_batch_producer.go](../transaction_batch_producer.go#L25)) | `applied=1`, `applyMode=unavailable` | V1 |
| Read batch size | `reader.readBatchSize` | Константа `50000` ([transaction_batch_producer.go](../transaction_batch_producer.go#L14)) | Можно показать read-only | V1 |
| Reader read TPS | `reader.readTps` | Будущая точка учёта после фактического чтения rows | Точного измерения нет; legacy `actualTPS` имеет другую семантику | V4 |
| Rows read | `reader.rowsReadTotal` | Будущий reader owner | `totalTransactions` считается после consumer и не является rows-read counter | V4 |
| Current sources | `reader.currentSources` | Producer владеет локальным `filePath` ([transaction_batch_producer.go](../transaction_batch_producer.go#L29)) | Известен glob `dataPath`, но активные файлы не публикуются | V4 |
| Admitted TPS | `throttler.admittedTps` | Будущая точка учёта на выходе throttler | Нет данных, потому что throttler выключен из тракта | V2 |
| Limiting/limited totals | `throttler.limiting`, `limitedTransactionsTotal`, `limitedMsTotal` | Throttler owner | Измерений ожидания нет | V2 |
| Queue 1 capacity | `queues[reader-to-throttler].capacity` | Текущий `batches` channel имеет capacity 2 ([transaction_batch_producer.go](../transaction_batch_producer.go#L13)) | Возможна только временная read-only проекция; channel ведёт сразу в black-hole consumer | V4 |
| Queue 1 depth | `queues[reader-to-throttler].depthBatches` | Можно сэмплировать `len(batches)` | Это мгновенная приблизительная глубина legacy channel, не контрактная очередь reader-to-throttler | V4 |
| Queue 1 queued transactions | `queues[reader-to-throttler].queuedTransactions` | Будущий queue owner | Нельзя получить из channel без дополнительного учёта размеров batch | V4 |
| Queue counters/rates | `enqueued*`, `dequeued*`, `inputTps`, `outputTps`, `throughputTps` | Точки учёта enqueue/dequeue | Сейчас отсутствуют | V4 |
| Queue blocked state | `blockedSenders`, `oldestBlockedSenderMs`, `blockedMsTotal`, `flowState` | Queue owner или инструментированный send wrapper | Producer может блокироваться на send, но ожидание не измеряется ([transaction_batch_producer.go](../transaction_batch_producer.go#L42)) | V4 |
| Queue 2 | `queues[throttler-to-sender].*` | Будущая очередь между throttler и sender | Стадия и очередь отсутствуют | V5 |
| Sender controls | `sender.workers`, `httpBatchSize`, `timeoutMs` | Будущий sender pool owner | Controls unavailable | V5 |
| Sender telemetry | `sender.attemptedTps`, `inFlightRequests`, response/retry totals | Sender pool owner | HTTP sender отсутствует | V5 |
| HTTP link | `http.lastStatusCode`, `throughputTps`, `inFlightRequests`, request totals, `latencyP95Ms` | HTTP client/instrumentation owner | Connection disconnected; измерения неизвестны | V5 |
| Target endpoint | `target.endpoint` | Будущая конфигурация sender/control loop | Endpoint отсутствует | V5 |
| Target telemetry | `target.acceptedTps`, `latencyP95Ms`, HTTP response totals | Sender может считать успешные batch transactions и ответы | Сейчас отсутствует | V5 |
| Target delay/error controls | `target.artificialDelayMs`, `errorRatePercent` | Только target emulator | Simulation-only; в реальном backend должны быть unavailable | V6 optional |

## 3. Что можно реализовать немедленно

Без изменения pipeline можно реализовать полный wire snapshot с честной доступностью:

- `schemaVersion`, `snapshotRevision`, `controlRevision`, `observedAt`, `samplePeriodMs` и `rateWindowMs`;
- `run.totalTransactions` из `generatorState.totalTransactions`;
- фиксированные `reader.workers.applied=1` и `reader.readBatchSize.applied=50000` с `applyMode=unavailable`;
- сохранённый configured requested TPS, с явной оговоркой, что он ещё не является эффективным throttling limit;
- известный source glob как диагностическую информацию, но не как `currentSources`;
- полные actor/queue/HTTP/target sections с `null` для неизвестной telemetry и `unavailable` для отсутствующих controls;
- `GET /api/v1/loadgen/snapshot` и frontend `HttpAdapter`, выполняющий wire-to-view-model mapping.

Legacy `statusSnapshot` использует строковые `targetTPS`, `actualTPS` и `totalTransactions` ([cmdgen.go](../cmdgen.go#L31)). Новый контракт требует числовые значения. Существующий `actualTPS` вычисляется из `consumedSinceTick` один раз в секунду ([main.go](../main.go#L73)) и отражает скорость транзакций, обработанных `consumeTransaction` ([transaction_batch_consumer.go](../transaction_batch_consumer.go#L9)). Его нельзя без переименования точки измерения подставлять в `reader.readTps`, `throttler.admittedTps` или `sender.attemptedTps`.

## 4. Что сейчас является simulation-only

До появления соответствующих Go-владельцев следующая telemetry должна быть `null`, а controls должны иметь `applyMode=unavailable`:

- изменяемое число reader workers;
- runtime read batch size;
- admitted/limited throttler telemetry;
- runtime queue capacity и вся точная queue telemetry;
- вторая очередь;
- sender workers, HTTP batch size и timeout;
- attempted TPS, in-flight requests, responses и retries;
- HTTP status, throughput, request counters и p95 latency;
- target endpoint и target telemetry;
- artificial delay и error rate target emulator.

Simulation генерирует endpoint, target latency, HTTP outcomes, queue telemetry и retries независимо от Go ([frontend/src/adapters/SimulationAdapter.ts](../frontend/src/adapters/SimulationAdapter.ts#L173)). При подключении backend нельзя заменять неизвестные значения синтетическим нулём: по контракту `0` означает измеренное отсутствие активности, а `null` означает отсутствие измерения ([docs/frontend-backend-contract.md](frontend-backend-contract.md#L44)).

## 5. Специальная проверка семантик

### Capacity 0

Контракт определяет capacity `0` как unbuffered rendezvous: depth и queued transactions равны нулю, но throughput может быть высоким ([docs/frontend-backend-contract.md](frontend-backend-contract.md#L170)). Simulation реализует это отдельным прямым `handoff` для обеих очередей ([frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L506), [frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L536)) и проверяет тестом ([frontend/src/adapters/SimulationAdapter.test.ts](../frontend/src/adapters/SimulationAdapter.test.ts#L376)).

Текущий Go channel имеет фиксированную capacity 2. Новый unbuffered channel даст правильный rendezvous, но существующий channel нельзя resize во время работы. Семантика уменьшения capacity ниже depth с pending/after-drain требует отдельного queue owner/event loop либо собственной bounded queue, которая сериализует enqueue, dequeue и configuration changes.

### Blocked senders

`blockedSenders` по документу означает точное число goroutine, прямо сейчас ожидающих отправки. Значения `len==cap` или `depth==capacity` этого не доказывают: очередь может быть полной, пока ни один sender ещё не начал send.

Simulation хранит только boolean `0/1`, то есть «upstream stage не смог передать», а не точное число goroutine ([frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L210)). `oldestBlockedSenderMs` там является длительностью непрерывного simulated blockage, а `flowState=backpressure` включается после 300 ms. Реальному backend нужны регистрация начала и завершения каждого ожидания, current count, timestamp старейшего активного ожидания и накопление blocked duration.

### Sender workers и in-flight

Simulation трактует каждый sender worker как один concurrency slot: доступные слоты равны worker count минус число in-flight requests ([frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L590)). Для Go wire следует различать:

- configured/applied worker count;
- фактически активные workers во время pending scale-down;
- `sender.inFlightRequests` и совпадающее `http.inFlightRequests` как число текущих HTTP requests.

Текущий frontend рисует workers в диапазоне 1-7 и блокирует кнопки только по этому локальному диапазону ([frontend/src/components/pipeline/WorkerActor.tsx](../frontend/src/components/pipeline/WorkerActor.tsx#L35)). Кнопки не учитывают backend `applyMode=unavailable`, поэтому read-only workers требуют frontend-коррекции.

### TPS rates

Simulation использует последовательные точки измерения:

- queue 1 input как `reader.readTps`;
- queue 1 output как `throttler.admittedTps`;
- queue 2 output как `sender.attemptedTps`;
- HTTP started transactions как HTTP link throughput;
- HTTP succeeded transactions как target accepted TPS ([frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L423)).

Эту схему стоит сохранить в Go, рассчитывая все display rates по одному окну. Prometheus counters остаются источником benchmark-анализа. Simulation использует фиксированное окно 1 s, но делит на полную секунду даже до накопления 100 шагов; первые значения поэтому занижены. Кроме того, её `limitedMs` увеличивается при reader saturation и любом queue blockage ([frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L366)), тогда как документ требует считать throttler limiting только когда причиной ограничения является сам throttler.

### SSE

SSE отсутствует в Go и React. Интерфейс `LoadgenAdapter.subscribe` уже подходит как frontend-граница ([frontend/src/adapters/LoadgenAdapter.ts](../frontend/src/adapters/LoadgenAdapter.ts#L10)), но нужен реальный adapter с `EventSource`, reconnect и фильтрацией возрастающих revisions.

Backend handler `GET /api/v1/loadgen/events` должен:

- отправлять событие `snapshot` с `id=snapshotRevision`;
- немедленно отправлять последний полный snapshot новому или переподключившемуся клиенту;
- завершать subscription по request context;
- не блокировать control loop медленным клиентом;
- использовать latest-value buffer на подписчика, заменяя устаревший непрочитанный snapshot;
- не хранить журнал дельт, поскольку MVP этого не требует ([docs/frontend-backend-contract.md](frontend-backend-contract.md#L40)).

## 6. Команды UI и необходимые backend messages

React-модель содержит 11 отправляемых типов команд ([frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L138)). Реальные места dispatch находятся в top bar, inspector и pipeline controls ([frontend/src/components/LabShell.tsx](../frontend/src/components/LabShell.tsx#L75), [frontend/src/components/LabShell.tsx](../frontend/src/components/LabShell.tsx#L159), [frontend/src/components/LabShell.tsx](../frontend/src/components/LabShell.tsx#L377)).

| UI command | Нужный Go owner/message | Текущее состояние |
|---|---|---|
| `run` | `applyCommand` → lifecycle `run` | Отсутствует |
| `pause` | `applyCommand` → lifecycle `pause` | Отсутствует |
| `reset` | `applyCommand` → lifecycle `reset` с проверкой run state | Отсутствует |
| `set-requested-tps` | `applyCommand` → throttler `setTPS` | Legacy `setTPS` есть, но throttler не подключён |
| `set-worker-count` reader | Reader pool configuration | Reader pool отсутствует |
| `set-worker-count` sender | Sender pool configuration | Sender отсутствует |
| `set-queue-capacity` | Queue owner configuration с immediate/pending receipt | Runtime-resizable queue отсутствует |
| `set-read-batch-size` | Reader owner configuration | Значение является локальной константой |
| `set-http-batch-size` | Sender owner configuration новых requests | Sender отсутствует |
| `set-http-timeout` | Sender owner configuration новых requests | Sender отсутствует |
| `set-target-delay`, `set-target-error-rate` | Target emulator owner | Только simulation |

Документ дополнительно определяет `set-target-endpoint`, но в `LoadgenCommand` такой команды нет ([docs/frontend-backend-contract.md](frontend-backend-contract.md#L221)).

Нужные transport/control-plane сообщения:

- `GET /api/v1/loadgen/snapshot`: чтение последнего immutable snapshot либо `getSnapshot` request владельцу;
- `POST /api/v1/loadgen/commands`: validation envelope, затем единое `applyCommand` с `commandId`, optional `expectedControlRevision` и reply channel;
- lifecycle/configuration messages сериализуются одним control loop;
- публикация нового snapshot происходит после каждой применённой команды и по telemetry ticker;
- SSE subscribe/unsubscribe обслуживает broadcaster, а не pipeline goroutine.

Сейчас Go понимает только legacy `targetTPS`, `quit` и внутренний `getStatus` ([cmdgen.go](../cmdgen.go#L12)). Текущие React-компоненты игнорируют возвращаемые receipts и ошибки: вызовы `dispatch` выполняются через `void`.

## 7. Минимальный порядок вертикальных срезов

1. **V0 — нормализация контракта.** Зафиксировать отдельные Go wire DTO и frontend wire-to-view-model mapper. Не сериализовать TypeScript view model напрямую.
2. **V1 — read-only snapshot.** Реализовать полный `GET /api/v1/loadgen/snapshot`, заполняющий total и фиксированные controls, остальные значения возвращать как unavailable/null. Добавить `HttpAdapter` и загрузку snapshot после reload.
3. **V2 — эффективный TPS control.** Включить throttler в реальный pipeline, добавить `set-requested-tps`, command envelope, receipt, snapshot/control revisions и семантику TPS `0`.
4. **V3 — lifecycle.** Ввести общий context, control-owned `run/pause/reset`, active elapsed time, сохранение queues/counters на pause и запрет reset во время running.
5. **V4 — reader и первая очередь.** Добавить точные reader counters, source ownership, enqueue/dequeue accounting, blocked waits, unbuffered capacity `0` и pending decrease after drain.
6. **V5 — sender, вторая очередь и HTTP.** Реализовать sender pool, bounded concurrency, HTTP batch/timeout, in-flight, outcomes, retries, latency и target-derived telemetry.
7. **V6 — SSE и optional emulator controls.** Подключить full-snapshot broadcaster и `EventSource`; target delay/error делать writable только при реально запущенном emulator.

SSE технически можно добавить раньше V5, но после V1-V3 уже будет устойчивый snapshot publisher, revisions и немедленная публикация после команд. Это уменьшает количество временных transport-решений.

## 8. Расхождения документа и фактического frontend

1. Документ задаёт `schemaVersion`, `snapshotRevision`, `controlRevision`, `observedAt`, `samplePeriodMs`, `rateWindowMs` и вложенный объект `run`; frontend имеет `revision` и плоские `runState`, `elapsedMs`, `totalTransactions` ([frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L122)).
2. Документ задаёт массив `queues`; frontend использует отдельные `queue1` и `queue2`.
3. Имена расходятся: `rowsRead`/`rowsReadTotal`, `source`/`currentSources`, `limitedMs`/`limitedMsTotal`, `blockedMs`/`blockedMsTotal`, `statusCode`/`lastStatusCode`, response/retry counters без/с суффиксом `Total`.
4. Frontend одновременно содержит `inputTransactionsPerSecond`/`outputTransactionsPerSecond` и дублирующие `inputTps`/`outputTps`; документ определяет только один набор transaction rates.
5. Frontend `ApplyMode` не содержит `after-drain`; `NumericControlSnapshot` не содержит `writable` и `unavailableReason` ([frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L17), [frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L28)).
6. Simulation выставляет pending capacity, но control и receipt продолжают сообщать `applyMode=immediate` ([frontend/src/adapters/SimulationAdapter.ts](../frontend/src/adapters/SimulationAdapter.ts#L65), [frontend/src/adapters/SimulationAdapter.ts](../frontend/src/adapters/SimulationAdapter.ts#L398)).
7. Preview по документу является frontend-only, но сейчас включён в общий snapshot control type и формируется adapter-ом.
8. Frontend command не содержит `commandId` и `expectedControlRevision`; `CommandReceipt` не содержит `status`, `acceptedAt` и `controlRevision` ([frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L138), [frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L158)).
9. Набор frontend error codes уже и одновременно содержит simulation-specific `disposed`, которого нет в wire-контракте.
10. Frontend `RunState` не поддерживает `stopping` и `failed` ([frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L15)).
11. Simulation разрешает `reset` во время running и переводит run в idle, хотя документ требует отклонять такой reset ([frontend/src/adapters/SimulationAdapter.ts](../frontend/src/adapters/SimulationAdapter.ts#L302), [docs/frontend-backend-contract.md](frontend-backend-contract.md#L261)).
12. Simulation нормализует, округляет и ограничивает numeric commands вместо backend-style validation rejection ([frontend/src/adapters/SimulationAdapter.ts](../frontend/src/adapters/SimulationAdapter.ts#L78)).
13. Документ требует `null` для неизвестной telemetry, но queue и HTTP counters во frontend типизированы только как `number` ([frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L57), [frontend/src/model/loadgen.ts](../frontend/src/model/loadgen.ts#L97)).
14. Numeric controls блокируются только через `applyMode=unavailable`, а не через документированное поле `writable` ([frontend/src/components/NumericControl.tsx](../frontend/src/components/NumericControl.tsx#L51)).
15. Pipeline worker buttons не учитывают ни `applyMode`, ни `writable`; они используют локальные пределы 1-7.
16. Команда `set-target-endpoint` присутствует в документе и отсутствует во frontend.
17. Frontend не хранит command errors, не показывает pending receipts, не сериализует быстрые команды одного регулятора и не фильтрует snapshots по revision.
18. Simulation snapshot публикуется каждые 100 ms, документ требует 200 ms ([frontend/src/model/simulation.ts](../frontend/src/model/simulation.ts#L3), [docs/frontend-backend-contract.md](frontend-backend-contract.md#L42)).
19. `main.tsx` всегда создаёт `SimulationAdapter`; `HttpAdapter` и `EventSource` отсутствуют.
20. Go server обслуживает старый `ui/app.js`, который polling-ом читает `/status` и пишет `/control`, а не React frontend и новые API endpoints ([ui/app.js](../ui/app.js#L172), [ui/app.js](../ui/app.js#L181)).

## Итоговая рекомендация

Ближайший безопасный шаг — V0 и V1: согласовать отдельный wire DTO, сделать полный read-only snapshot с честными `null`/`unavailable` и подключить React через `HttpAdapter`. После этого отдельным завершённым срезом включить throttler в фактический pipeline и только тогда объявлять requested TPS применённым control. Такой порядок сразу даёт работающую frontend/backend связь и не закрепляет ложную семантику текущего `actualTPS` или неработающего TPS limit.
