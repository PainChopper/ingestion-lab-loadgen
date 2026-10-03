# План реализации рефакторинга ingestion-lab

Статус: решения для реализации; выполненное отмечено отдельно.
Создан: 2026-10-02.

## Цель

Упростить текущую реализацию продукта: убрать лишнее состояние, передачу одних
и тех же зависимостей, искусственные ветки и обёртки. Основание каждого изменения —
нынешняя обязанность кода. Будущие требования и точки расширения не добавлять.

Этот документ задаёт решения, границы и проверки следующих изменений.
По просьбе владельца рекомендации аудита оформлены как решения для реализации,
без дополнительного этапа выбора для каждого пункта. При создании этого документа
работа была ограничена планом; последующая реализация отмечается ниже. Каждый шаг имеет
согласованный diff и проверку затронутого поведения; обязательного порядка нет.

Основание пунктов 2–10 — [отчёт T8124 с дополнением T8125](../../../../ingestion-lab-agents-runtime/MAIL/REVIEWER/OUT/T8124_20261002-0000_REVIEWER_product-simplification_report.md).
Это статический аудит: в дополнении заявлено полное чтение Go-кода и тестов двух
модулей; будущие изменения ещё не проверены выполнением. Полный frontend-аудит
в дополнение не входил. Карта файлов и доказательства остаются в исходном отчёте.

Пункт 11 основан на [анализе T8128 — прямой Reader pool](../../../../ingestion-lab-agents-runtime/MAIL/ANALYST/OUT/T8128_20261002-0000_ANALYST_reader-runtime_report.md).
Пункт 12 использует [анализ T8127 — Pause перед HTTP-попытками Sender](../../../../ingestion-lab-agents-runtime/MAIL/ANALYST/OUT/T8127_20261002-0000_ANALYST_run-pause_report.md).
Последующие прямые уточнения владельца имеют приоритет: обычный затвор перед
HTTP attempts, включая retry; текущая отображаемая картинка метрик фиксируется
на Pause, внутренний учёт доставок продолжается. Точность до миллисекунд,
особая гарантия физического сетевого старта и привязка к точному времени клика
не требуются. Устаревшие ссылки отчёта на reuse не применять: пункт 10 завершён.

Исходная сверка с LEAD 2026-10-02, baseline `566af63` (актуальное исполнение ниже):

- `f1a46a1`: уже выполнены группировка полей в readerRun и удаление throttler ack.
  Группировка в readerRun не является реализацией прямого pool из пункта 11.
- `566af63`: реализованы fresh channels после Reset и переименование readerBatches;
  владелец принял изменение, коммит запушен. Пункт 10 закрыт.
- На момент исходной сверки пункты 2–9 и 11 ещё не были реализованы.
- Sender-only Pause выбрана владельцем, но ещё не реализована. Это пункт 12.
- «Оставить функции throttler» — рекомендация LEAD, не решение владельца.
  Владелец сохранил исходную задачу перехода к методам и проверки уже доступных
  зависимостей. Конкретная форма реализации задана в пункте 1.
- Уточнения владельца отменяют обязательный порядок и сохранение существующих
  test seams как самостоятельную цель. Рабочий код определяет тесты.

## Состав работы и текущие статусы

| Пункт | Содержание | Состояние |
|---|---|---|
| 1 | Конкретная стадия throttler со своими методами | Реализовано в T8138 и принято владельцем; paused удаляется по пункту 12 |
| 2 | Удаление senderTelemetry | Реализовано в T8140 с проверками T8142; принято владельцем, коммит 3f393e0 |
| 3 | Один рабочий путь Reader вместо nil-worker ветки | Реализовано в T8145 и принято владельцем; коммит d986614 |
| 4 | Общий контекст Reader pool вместо копии в worker | Реализовано в T8147 и принято владельцем; коммит 87f233e |
| 5 | Удаление лишних child contexts в runtime | Реализовано в T8148; адресные/test/race/vet/build пройдены, commit/push по поручению владельца |
| 6 | Одна категория ошибки доставки Sender | Реализовано напрямую по поручению владельца; Sender/test/race/vet/build пройдены |
| 7 | Вывод runtimeStatus без промежуточной runtimeSummary | Реализовано в T8149; CLI/log/test/race/vet/build пройдены |
| 8 | Прямые обращения вместо пустых forwarding-функций | Завершено в T8151; validation/persistence/test/race/vet/build пройдены |
| 9 | Удаление двух неиспользуемых полей PrometheusMetrics | К реализации |
| 10 | Новые каналы после Reset | Реализовано в HEAD 566af63 |
| 11 | Прямой Reader pool вместо readerRun | Реализовано; readerStarter и подмена запуска полностью удалены |
| 12 | Pause живого Sender и фиксация отображаемых метрик | Требования владельца приняты; к реализации |
| 13 | Аналитический аудит избыточных объёмов тестовых данных | Анализ T8144 завершён; сокращение T8146 принято владельцем, коммит 1b63b90 |

### Выполнено 2026-10-03

- Пункт 8 завершён в T8151: семь getters и три valid wrappers заменены
  прямыми controls/contains, sender capacity validator заменён validate.
  Три опустевших production-файла и два wrapper-only tests удалены;
  HTTP/direct validation, idle-only/persistence и business assertions сохранены.
  Адресные tests, общие test/race, vet, build, gofmt и diff-check прошли.
  Подготовительный T8150 завершился до правок из-за ошибочного необязательного
  пути README; Go checks в нём не запускались, исторический OUT сохранён.
- Пункт 7 реализован в T8149: runtimeSummary и преобразователь удалены;
  карточка CLI и выбранные поля периодического лога читают runtimeStatus напрямую.
  SourceError берётся из Reader; format/golden/sanitize, список полей, событие
  runtime_summary и минутный интервал сохранены. Четыре Go-файла, существующие
  tests адаптированы без новых seams. CLI/log tests, общие test/race, vet,
  build, gofmt и diff-check прошли; commit/push поручены владельцем.
- Пункт 6: terminal/retryable failure объединены в senderAttemptFailure.
  Только HTTP 204 означает успех; остальные ответы и ошибки сохраняют retry,
  отмена остаётся отдельным исходом. Закрытие response body и задержки сохранены.
  Существующие тесты адаптированы, terminal-название переименовано; новые тесты
  не добавлены. Адресные Sender tests, общие test/race, vet, build, gofmt
  и diff-check прошли. Небольшая механическая правка выполнена непосредственно
  по просьбе владельца, без отдельного кодера или аналитика.
- Пункт 5 реализован в T8148: Reader и throttler получают runContext напрямую;
  cancelReader/cancelThrottler и создание child contexts удалены. Контексты pools,
  startup cleanup и порядок stopSender → cancelRun → joins → очистка сохранены.
  Изменён только pipeline_runtime.go; адресные lifecycle tests, общие test/race,
  vet, build, gofmt и diff-check прошли. Commit/push поручены владельцем для
  каждого пункта плана; отдельная независимая приёмка не проводилась.
- Пункт 4 реализован в T8147: readerWorker.ctx и его initializer удалены;
  чтение, append, send, blocked wait и cleanup используют pool.ctx.
  closeResources больше не принимает worker; дублирующая проверка отмены
  схлопнута. Существующие тесты используют контекст pool, включая отмену
  blocked-send. Draining, остатки файлов, blocked/reading, source errors
  и первая ошибка закрытия сохранены. Адресные Reader tests, общие test/race,
  vet, build, gofmt и diff-check прошли. Предыдущая правка пункта 13 сохранена.
  Результат принят владельцем, коммит 87f233e; пункт 5 и новая
  Pause этим этапом не затронуты, отдельные analyst/tester/reviewer не запускались.
- Пункт 3 реализован в T8145 по анализу T8143: удалены forwarding appendRows
  и nil-worker ветки; один appendRows(worker, batch, rows) и рабочий sendBatch.
  Существующий тест коротких чтений создаёт worker с контекстом и состоянием
  pool, отправляет остаток тем же путём. Проверки размеров 2/1 и порядка строк,
  контексты, отмена, блокировка и владение срезами сохранены. Изменены только
  reader_pool.go и reader_pool_test.go. Адресные TestReaderPool, общие test/race,
  vet, build, gofmt и diff-check прошли. Владелец принял результат и поручил
  commit/push; реализация сохранена в d986614. Tester/reviewer не запускались.
- Пункт 13: анализ T8144 охватил 27/27 Go test-файлов loadgen и receiver
  (176 Test-функций). Один малый кандидат — 20 → 2 последовательных цикла
  в TestReaderPoolOwnsFilesAcrossConcurrentCycles; данные и требования других
  сценариев не дают обоснованного безопасного сокращения. Код по этому пункту
  не менялся при анализе. Владелец согласовал сокращение 20 → 2 циклов;
  T8146 реализовал ровно эту числовую замену, сохранив все assertions,
  два пути и два worker. Адресный тест, gofmt и diff-check прошли.
  Другие объёмы не менялись; результат принят владельцем, коммит 1b63b90.
- Пункт 2 реализован в T8140: удалены приватный sender batch-счётчик и его тест,
  ссылки, параметр запуска и сбросы. Проверки cancellation оставляют утверждение
  `consumed == 0`; accepted-batch stop сохраняет учёт и join. Metrics test не менялся:
  при одном worker вход второго реального HTTP-запроса следует за успешным ответом
  первого и доказывает завершение его batch; второй запрос удерживается до metrics tick.
  Адресные тесты, `go test ./...`, `go vet ./...` и build прошли. Полный
  `go test -race ./...` один раз упал в `TestReadBatchSizeIdleOnlyAndPersistsAfterReset`
  (Reader не выдал batch); отдельный запуск этого теста прошёл. Последующий T8142
  уменьшил только фикстуру этого теста: batch 25 000 → 2 000, файл 50 000 → 4 000
  строк; проверки применения размера, idle-only и сохранения после Reset,
  deadlines и большие validation-значения сохранены. Адресный race-прогон
  с `-count=20` и полный `go test -race ./...` прошли. Изменение T8142 принято
  владельцем; коммиты `3f393e0` (пункт 2) и `210ec84` (фикстура) запушены
  в origin/main. Причина прежнего таймаута не доказана; успешные прогоны
  с меньшими данными не подтверждают гипотезу T8141 о конкуренции с Throttler.
  Отдельное reviewer-ревью не проводилось; исторические отчёты и статусы сохранены.
- Новая форма пункта 1 реализована в T8138: конкретный throttler хранит направленные
  ссылки на очереди, указатели telemetry, updates/done; settings локальны в worker.
  Runtime сохраняет нынешние contexts/cancel/join/queues. Существующие тесты
  адаптированы; адресные, общие test/race/vet/build, gofmt и diff-check прошли.
  Проверки кодера пройдены; новая форма принята владельцем 2026-10-03.
  Отдельное reviewer-ревью новой формы не проводилось.
- Пункт 1: методы Throttler используют каналы и telemetry существующего runtime,
  настройки остаются локальными в горутине. throttlerStarter удалён полностью,
  без замещающего механизма. Выполнение T8129/T8131, review T8130/T8132.
  Это исторический результат до T8138: прежние review не подтверждают новую форму.
- Пункт 11: runtime хранит прямые `readerPool *readerPool` и `senderPool *senderPool`;
  readerRun удалён. Дополнительно по решению владельца удалены readerStarter,
  runtime.read и передача функции запуска: runtime вызывает state.startReaderPool.
  Выполнение T8131/T8135; T8132 принял прямые pools, T8135 передан владельцу.
- В рамках пункта 8 удалена использовавшаяся только тестами обёртка eventLoop.
  Тесты вызывают eventLoopContext с явным контекстом. Остальная часть пункта 8
  не объявляется выполненной.
- Отдельное принятое упрощение: неизменяемая загруженная конфигурация перенесена
  из configuredControls в state.config; controls хранят текущие изменяемые значения.
  Начальные значения, TOML/JSON и поведение сохранены. Выполнение T8133, review T8134.
- Существующие тесты запуска Reader переведены на настоящие временные Parquet
  и каналы. Удалены искусственные callbacks и задержка join через добавленную
  тестом goroutine; реальные ошибки источника, Reset, done/cleanup, batch size
  и capacity проверяются рабочим путём. Новых подмен и production hooks нет.
- Финальные проверки T8135: go test ./..., go test -race ./..., go vet и build
  завершились с exit code 0; gofmt и git diff --check также успешны.

Доказательства: [T8132](../../../../ingestion-lab-agents-runtime/MAIL/REVIEWER/OUT/T8132_20261002-0000_REVIEWER_direct-runtime-pools_report.md),
[T8134](../../../../ingestion-lab-agents-runtime/MAIL/REVIEWER/OUT/T8134_20261002-0000_REVIEWER_state-config_report.md),
[T8135](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8135_20261003-0000_CODER_remove-reader-start-seams_report.md).

Пункты 9 и 12 не выполнены. Пункты 2, 5–8 и 10 завершены;
пункты 3 и 4 приняты владельцем. Пункт 13 завершён, сокращение принято.
Сохранение paused в Throttler сейчас необходимо для прежней Pause; удаление
выполняется вместе с новым затвором Sender в пункте 12.

Владелец не устанавливает обязательный порядок. Нумерация — адреса пунктов,
а не последовательность исполнения. Предложения LEAD делать 3 перед 4 и 11 перед 5
можно использовать для удобства, но они не являются зависимостями реализации.
Связанные обращения и тесты согласовывать в каждом diff; пункты можно объединять
или выполнять отдельно. Переход к методам не требует готовности новой Pause.
Удаление throttler.paused согласовать с появлением Pause в Sender, чтобы не оставить
промежуточную версию без паузы. Пункт 10 не выполнять повторно.

## 1. Конкретная стадия throttler со своими методами

Статус: реализовано в T8138, проверки пройдены; принято владельцем 2026-10-03.
Прежняя форма методов runtime выполнена исторически. Новая форма основана на
[T8136](../../../../ingestion-lab-agents-runtime/MAIL/ANALYST/OUT/T8136_20261003-0000_ANALYST_concrete-throttler_report.md)
и [T8137](../../../../ingestion-lab-agents-runtime/MAIL/ANALYST/OUT/T8137_20261003-0000_ANALYST_throttler-settings_report.md).
Владелец подтвердил сохранение throttlerSettings. Перенос Pause в Sender
выполняется отдельно по пункту 12.

### Конкретная форма и владение

```go
type throttler struct {
    readerBatches <-chan []Transaction
    senderBatches chan<- []Transaction
    readerChannel *channelTelemetry
    senderChannel *channelTelemetry
    updates       chan throttlerSettings
    done          chan struct{}
}

func (t *throttler) start(ctx context.Context, initial throttlerSettings)
func (t *throttler) forwardBatch(
    ctx context.Context,
    batch []Transaction,
    settings *throttlerSettings,
) bool
func (t *throttler) update(settings throttlerSettings)
```

- Runtime после успешного state.startReaderPool собирает конкретную стадию
  с направленными ссылками на уже созданные очереди и указателями на telemetry.
  Хранит throttler *throttler вместо отдельных done/updates.
- start синхронно создаёт небуферизованные updates и done, запускает одну горутину.
  settings := initial и удерживаемый batch остаются локальными; done закрывается
  при выходе. Объект запускается один раз; новый Run создаёт новый объект.
- forwardBatch использует зависимости receiver и указатель только на локальное
  settings той же горутины. Ссылки на runtime/state/config, shared applied fields,
  constructor/options/interface, wrappers и тестовые подмены не добавлять.
- throttlerSettings сохраняется как небольшое полное value-message с requestedTPS,
  mode и временным paused. Не добавлять dependencies и partial-command protocol.
- update сохраняет select send/done и no-op при nil receiver. Event loop обращается
  к runtime.throttler.update напрямую; runtime.updateThrottler удаляется.
  Updates не закрывать; buffer/ack не добавлять.
- Context передаётся явно. Пока пункт 5 не выполнен, runtime передаёт нынешний
  child context; стадия не хранит context/cancel и не создаёт новых contexts.
- Runtime сохраняет cancel/join и владение общими очередями: отменяет стадии,
  ждёт Reader и throttler.done, затем close/drain/detach и очистка ссылки стадии.
  Стадия не создаёт/не закрывает очереди и не начинает/не сбрасывает telemetry.
  Ссылки каналов и адреса telemetry неизменны до join.

### Сохранение поведения и проверка

Начальные настройки собирает владелец controls до запуска. Последующие полные
сообщения применяет единственная рабочая горутина при ожидании input, zeroTPS,
pacing timer и blocked output. Сохранить порядок batches, pacing и перезапуск
задержки после update, held batch, bypass, blocked telemetry, input closure
и cancellation. Рабочую Pause через paused сохранить до Sender gate по пункту 12;
только вместе с ним убрать поле и pause-specific updates.

Основные файлы: throttler.go, pipeline_runtime.go, четыре обращения event_loop.go,
существующие throttler/lifecycle-тесты. Тесты стадии собирают конкретный throttler
с реальными очередями и telemetry, без неполного runtime-harness. Существующий
lifecycle-тест захватывает throttler.done до stop и проверяет nil stage после stop.
Новых тестов ради формы методов/числа параметров и новых seams не добавлять.
Child contexts и новая Pause не входят в этот этап; Reset не выполнять повторно.

Критерий завершения: конкретная стадия не ссылается на runtime/state; settings
локальны, зависимости направлены; runtime lifecycle и общие очереди сохранены.
Выполнить существующие затронутые и общие Go-проверки, включая race.

## 2. Удалить неиспользуемый счётчик Sender

**Зачем.** `senderTelemetry.completedBatches` обновляется под отдельным mutex
после успешной доставки, но его snapshot нужен только тестам. Продукт считает
доставленные транзакции через `terminallyCompletedTransactionsSinceTick`.

**Изменение.** Удалить `senderMeasurements`, `senderTelemetry`, ссылки на него,
параметр запуска Sender и сбросы этого счётчика. Оставить прямой путь:
успешная доставка → `true` → atomic Add числа транзакций → освобождение worker.

**Границы.** `sender_telemetry.go` и его тест, `sender_pool.go` и связанные тесты,
`sender_http_test.go`, `control_state.go`, `pipeline_runtime.go`, сбросы и metrics-тест
в `event_loop.go`/`event_loop_test.go`. Удаляем приватный счётчик батчей;
подсчёт транзакций, retry, HTTP-доставка и публичные метрики сохраняются.

**Метрик-тест.** `TestMetricsWindowDrivesChannelRatesAndActualTPS` уже использует
реальный HTTP-путь и одного worker: успешный ответ первого запроса предшествует
входу второго, поэтому второй запрос подтверждает завершение первого batch.
Второй handler удерживается до metrics tick, после чего тест проверяет накопленный
actual TPS. Этот существующий сценарий остаётся без изменений; дополнительное
ожидание idle, синхронизация или production ack не требуются.

**Проверка.** Metrics window/actual TPS, retry cancellation, отмена родительского
контекста и ожидание принятого батча при остановке. Убрать тест только удалённого
счётчика; сохранить проверки, что отменённая доставка не увеличила число транзакций.

## 3. Убрать тестовый путь Reader без worker

Статус: реализовано в [T8145](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8145_20261003-2225_CODER_reader-worker-path_report.md)
по [анализу T8143](../../../../ingestion-lab-agents-runtime/MAIL/ANALYST/OUT/T8143_20261003-2215_ANALYST_reader-worker-path_report.md).
Проверки пройдены; принято владельцем, коммит d986614. Пункт 4 не включён.

**Зачем.** `appendRows` вызывает `appendRowsForWorker(nil, ...)` только ради
одного теста. Рабочее чтение всегда передаёт настоящего worker; nil включает
отдельную отправку без учёта его активности.

**Изменение.** Оставить один `appendRows(worker, batch, rows)` и один путь
`sendBatch` с обязательным worker. Удалить forwarding-функцию и nil-ветки.
Существующий тест коротких чтений создаёт worker и проверяет рабочий путь.

**Границы.** `reader_pool.go` и `reader_pool_test.go`.

**Рекомендация кодеру.** Worker в тесте должен иметь необходимые текущему коду
контекст и состояние pool; одного ненулевого указателя недостаточно. Сохранить
отмену до отправки, изменение blocked под `pool.mu`, владение срезами и порядок
строк. Не заменять удалённую ветку новой тестовой функцией в production.

**Проверка.** Границы батча и остаток короткого чтения, отсутствие смешивания файлов,
мгновенная отправка, блокировка с последующим освобождением и отмена блокировки.

## 4. Удалить копию pool context из Reader worker

Статус: реализовано в [T8147](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8147_20261003-2246_CODER_reader-pool-context_report.md).
Проверки пройдены; результат принят владельцем, коммит 87f233e. Пункт 5 не включён.

**Зачем.** Каждый рабочий `readerWorker.ctx` получает ровно `pool.ctx`, отдельного
worker cancel нет. Двойные проверки контекста не описывают разные границы отмены.

**Изменение.** Использовать `pool.ctx` при чтении, отправке и cleanup. Удалить
поле worker context и параметр worker у `closeResources`, если других обязанностей
у этого параметра нет. Подготовку существующих тестов перевести на контекст pool.

**Границы.** `reader_pool.go` и `reader_pool_test.go`; отдельно от пункта 5.

**Рекомендация кодеру.** Мягкое уменьшение пула продолжает использовать draining:
worker заканчивает свой файл и не берёт следующий. Контекст и cancel самого pool
сохраняются для остановки и ошибки источника. Уточнить комментарии, которые
сейчас обещают отдельную отмену worker.

**Проверка.** Отмена освобождает заблокированных/draining workers; ошибки закрытия
при отмене игнорируются по прежним правилам, а без отмены сохраняется первая ошибка.
Поведение blocked/reading и остатки файлов не меняются.

## 5. Удалить лишние child contexts Reader и throttler в runtime

Статус: реализовано в [T8148](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8148_20261003-2256_CODER_runtime-run-context_report.md).
Адресные lifecycle tests, общие test/race, vet, build, gofmt и diff-check прошли.

**Зачем.** Runtime создаёт отдельные контексты двух стадий, но текущие пути
останавливают весь run. Их cancel не используется для самостоятельного управления
стадиями: остановка/Reset отменяют run, а ошибка старта также завершает весь run.

**Изменение.** Передавать стадиям `runContext`, удалить `cancelReader`/`cancelThrottler`
и связанные создание, вызовы и очистку. state.startReaderPool и throttler.start
запускаются напрямую с runContext; удалённые starters не восстанавливать.
До этого отдельного шага нынешние child contexts сохраняются.

**Границы.** `pipeline_runtime.go` и необходимые связанные проверки.

**Рекомендация кодеру.** Сначала сопоставить каждое создание контекста с каждым
местом отмены. Сохранить последовательность завершения: остановка Sender,
отмена run, ожидание Reader/throttler, затем очистка/закрытие каналов. Контексты
внутри Reader/Sender pools имеют собственные обязанности и этим пунктом не удаляются.
Принятая Pause из пункта 12 сохраняет contexts и не требует отдельной отмены стадий.

**Проверка.** Отмена приложения, наследование Sender контекста через Pause/Resume,
Reset с удерживаемым батчем при нулевом TPS, ошибка старта/источника и последующий Run.

## 6. Объединить одинаково обрабатываемые ошибки Sender

Статус: реализовано напрямую по поручению владельца. Адресные Sender tests,
общие test/race, vet, build, gofmt и diff-check прошли;
доказательства в runtime RUNLOGS/product-point6/.

**Зачем.** HTTP attempt возвращает terminal или retryable failure, но Sender pool
для обеих категорий выполняет одинаковый retry/backoff и одинаковую диагностику.

**Изменение.** Оставить результаты success, failure и canceled. Только 204
считается успешной доставкой; остальные ответы и прежние ошибки идут по тому же
пути повтора. Существующую отмену оставить отдельным исходом.

**Границы.** `sender_http.go`, `sender_pool.go` и их существующие тесты.

**Рекомендация кодеру.** Проверить также ошибки сериализации, создания запроса,
HTTP-клиента и чтения response body. Меняется классификация, а не судьба батча:
не превращать 4xx в отброс данных, не менять retry delays и не терять закрытие body.
Переименовать тесты, обещающие terminal-поведение, которого фактически нет.

**Проверка.** 204/не-204, повтор после 400/405/413 до успеха, сохранение того же
батча после исчерпания списка задержек и остановка при отмене.

## 7. Выводить runtimeStatus без промежуточной runtimeSummary

Статус: реализовано в [T8149](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8149_20261003-2304_CODER_direct-runtime-status_report.md).
Карточка/golden/sanitize, CLI status/exit codes, log fields/cadence/debug,
общие test/race, vet, build, gofmt и diff-check прошли.

**Зачем.** `runtimeSummaryFromStatus` перекладывает поля готового snapshot
в ещё одну структуру. Не вычисляет значения, не делает глубокую копию
и не обеспечивает отдельную синхронизацию. SourceError копируется повторно.

**Изменение.** Принимать `runtimeStatus` непосредственно в форматировании
CLI-карточки и выбранных полей логирования. Удалить тип `runtimeSummary`
и преобразование; source error брать из `status.Reader.SourceError`.

**Границы.** `runtime_summary.go`/`_test.go`, места вызова в `cli_remote.go`
и `event_loop.go`. Публичный snapshot и `runtime_status.go` не меняются.

**Рекомендация кодеру.** Сохранить явный выбор полей лога, очистку управляющих
символов, формат/округление CLI-вывода и период логирования. Не заменять
выборочный вывод логированием всей структуры вместе с config.

**Проверка.** Формат карточки и golden, очистка строк, один snapshot-запрос для
CLI status, прежние exit codes, logfmt fields/cadence и фильтрация debug-событий.

## 8. Убрать пустые forwarding-функции

Статус: завершено в [T8151](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8151_20261003-2315_CODER_direct-controls-followup_report.md).
Существующие business tests сохранены; адресные/full/race/vet/build/gofmt/diff-check прошли.

**Зачем.** Getters текущих controls, три setting wrappers и
`allowedConfig.validateSenderChannelCapacity` только возвращают поле или
переадресуют вызов. Дополнительной обработки или синхронизации у них нет.

**Изменение.** Использовать `state.controls.<field>`, соответствующий
`config.<setting>.contains(value)` и существующий `validate` непосредственно.
Удалить опустевшие файлы и тесты исключительно удалённых forwarding-функций.

**Границы.** `read_batch_size.go`, `reader_channel_capacity.go`,
`sender_channel_capacity.go`, соответствующие обращения в `event_loop.go`,
`pipeline_runtime.go`, `http_commands.go`, `config.go` и связанные тесты.

**Рекомендация кодеру.** Не удалять обработчики ошибок вместе с wrappers.
HTTP validation и event-loop validation остаются: это разные входы.
Прямые чтения controls выполняются у нынешнего владельца состояния;
не переносить их в рабочие горутины без синхронизации. Две тестовые convenience
функции `eventLoop`/`eventLoopWithThrottler` рассмотреть здесь отдельно: удалять,
только если прямые вызовы упрощают тесты без появления другого адаптера.

**Проверка.** Разрешённые/запрещённые значения, idle-only изменения, сохранение
выбранных настроек после Reset, прямые runtime commands и HTTP-ошибки.

## 9. Удалить неиспользуемые поля PrometheusMetrics

**Зачем.** `errorsTotal` и `parquetReadSeconds` только объявлены;
конструктор не создаёт и не регистрирует их, потребителей нет.

**Изменение и границы.** Удалить эти два поля в `prometheus_metrics.go`.
Три существующие зарегистрированные метрики сохранить.

**Проверка.** Поиск оставшихся обращений и существующие проверки метрик/компиляции.
Нового теста или новой регистрации метрик для этой правки не требуется.

## 10. Освобождать каналы на Reset, создавать заново на Run

Статус: реализовано в `566af63`. В текущем коде Reset использует `runtime.stop`,
новый Run создаёт обе очереди, reuse helpers и created flags удалены.
Прежняя отметка отчёта T8124 о приостановке T8126 устарела. По
[отчёту T8126](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8126_20261002-0000_CODER_fresh-channels_report.md)
до переименования владельца прошли test/race/vet/build. LEAD подтвердил повторный
go test после переименования; повторные race/vet/build после него не подтверждены:
владелец остановил кодера и поручил commit/push. Это существующие результаты,
независимо здесь не повторялись. Условия ниже сохраняются для последующих изменений.

**Зачем.** Сохранение физических каналов между завершёнными запусками экономит
повторную allocation, но требует отдельного reset-пути, reuse/replacement helpers,
created flags и тестов идентичности. Иного текущего потребителя identity не найдено.

**Результат.** Reset завершает run, дожидается горутин, закрывает/очищает каналы,
убирает ссылки и обнуляет прогресс. Следующий Run создаёт два новых канала
с выбранными capacity. Настройки сохраняются. Pause/Resume внутри одного запуска
сохраняет его очереди; семантика новой Pause зафиксирована в пункте 12.

**Границы.** `pipeline_runtime.go`, Reset в `event_loop.go` и непосредственные тесты.
Меняется приватная гарантия identity между run; новые allocations ожидаемы.

**Рекомендация кодеру.** Использовать общий teardown, если он уже выполняет
нужную остановку; не создавать ещё одну ветку полного завершения. Удалить
reuse helpers и created flags после упрощения ошибки старта. Сохранить ожидание
Reader и конкретной стадии throttler.done до close; новый Run создаёт новый объект.
Сохранить корректную очистку частично созданного запуска и повторный Reset.

**Проверка.** В смешанных тестах удалить assertions о сохранении физического
канала между run, сохранив проверки отсутствия старых батчей, нового producer,
capacity, обновлённого batch size и метрик. Отдельно сохранить assertions
Pause/Resume, проверить нулевой TPS, ошибку старта и отсутствие зависания teardown.
Не удалять целый behavioral-тест только потому, что одна его проверка устарела.

## 11. Хранить прямой Reader pool вместо readerRun

Основание: T8128, `CONCLUSIVE`, baseline `566af63908780be6852e8da2140fd4d15d7d5ea8`.
Статус: реализовано в T8131/T8135. Прямые pools приняты review T8132;
финальная адаптация тестов T8135 прошла обычные и race-проверки и передана владельцу.

### Решение и его основание

Удалить `readerRun`; в `pipelineRuntime` хранить `readerPool *readerPool`.
Функция создания возвращает `(*readerPool, error)`, а
`state.startReaderPool` возвращает созданный pool напрямую. По последующему
решению владельца readerStarter полностью удалён: запуск не подменяется.

Четыре поля `readerRun` — done, reconcile, aggregateSnapshot и sourceErrors —
только повторяют каналы и bound methods настоящего pool. Обёртка сама не создаёт
ресурсы, не отменяет работу, не ждёт завершения, не синхронизирует состояние
и не обрабатывает ошибку источника. Единственная рабочая сборка находится
в `control_state.go`; остальные сборки используются тестами.

Fallible startup, sourceErrors и сохранение Reader на Pause — реальные особенности
Reader, но существующий pool уже реализует их. Сходство с хранением `*senderPool`
полезно для чтения runtime, однако основание удаления — отсутствие обязанности
у wrapper, а не симметрия сама по себе.

### Изменение рабочего пути

| Путь | Что остаётся и что меняется |
|---|---|
| Успешный Run | Runtime создаёт context/очереди, state.startReaderPool создаёт полноценный pool, runtime сохраняет указатель, собирает конкретный throttler и запускает его. Контекст pool, cond, workers, done и watcher создаются прежним конструктором. |
| Ошибка старта | Glob error/пустой fixture возвращают `nil, error` до запуска ресурсов pool. Runtime выполняет прежний startup cleanup; event loop сохраняет source diagnostic и faulted. Не вводить частично стартовавший pool вместе с error. |
| Повторный Run, Pause/Resume | Reader создаётся только из idle. Внутри текущего run сохраняется тот же указатель; существующая семантика Pause не меняется этим пунктом. |
| Reset/shutdown | Сначала остановка/отмена, затем ожидание горутин, close/drain/detach очередей и `readerPool = nil`. Новый Run создаёт новый pool и новые очереди. Закрытие requests/application cancellation по-прежнему ведут к общему stop. |
| Ошибка источника во время Run | Pool передаёт одну ошибку через прежний буфер sourceErrors размером 1, отменяется и будит workers. Event loop сохраняет диагностику, faulted и выполняет stop/reset измерений. Правила закрытия файлов и выбора cleanup error остаются в pool. |
| Reconcile | После проверки `readerPool != nil` вызвать `readerPool.reconcile(workers)`. Mutex, safe downscale и отказ от reconcile после отмены/stop принадлежат pool. |
| Snapshot | После проверки указателя вызвать `readerPool.aggregateSnapshot()`; при nil вернуть прежний нулевой readerPoolSnapshot. Методы pool сами обеспечивают нужную синхронизацию. |
| SourceErrors select | При nil Reader вернуть nil channel, отключающий select case; иначе вернуть тот же `readerPool.sourceErrors`. После stop старый pool/канал больше не доступны runtime. |

### Фактический результат и действующие проверки

Runtime хранит readerPool *readerPool и senderPool *senderPool; state.startReaderPool
возвращает настоящий pool напрямую. readerRun, readerStarter и подмена создания
удалены в T8131/T8135. Повторная реализация Reader и восстановление seams не нужны.

Историческая карта подмен baseline T8128 заменена итогом T8135: существующие
event-loop, capacity, batch-size и runtime-summary сценарии используют реальные
Parquet sources, pools и очереди. Сохраняются startup/source faults, applied
workers/size/capacity, Pause/Resume identity, свежие очереди и очистка Reset.
Ручные producers, callbacks и искусственные join gates не восстанавливать.

При выделении throttler по пункту 1 runtime ждёт runtime.throttler.done;
Reader join остаётся runtime.readerPool.done. Отменить обе upstream стадии до
ожидания обеих; новые очереди и nil pointers появляются только после joins.
Удаление child contexts — отдельный невыполненный пункт 5.

### Обязательные условия и проверки

- Nil guards нужны в stop/join/reconcile/snapshot/sourceErrors; clearActive
  присваивает nil. Повторный stop безопасен.
- **Не закрывать sourceErrors при stop.** Нынешний pool этого не делает;
  closed channel постоянно выдавал бы zero-value errors в event-loop select.
  Отключение выполняется nil channel после удаления указателя на старый pool.
- Отменить обе upstream стадии до ожидания, дождаться обеих до close очередей.
  Reader done не доказывает завершения throttler; существующий Reset join-тест
  с настоящим Reader и захватом throttler.done сохраняется.
- После stop snapshot/reconcile/sourceErrors не обращаются к предыдущему pool.
  Ошибка старта оставляет reader nil; runtime startup cleanup и source diagnostics сохраняются.
- Существующие reader_pool_test.go проверки напрямую тестируют pool и сохраняются:
  safe downscale (:324, :396, :464), реактивация (:543), cancellation (:584),
  закрытие файла после отмены (:611), close errors (:640, :656).
- Сохранить реальные event-loop проверки corrupt parquet (:462) и startup glob
  failure (:507), всю карту поведения пяти test-файлов и проверку новых очередей Reset.
- После адаптации выполнить компиляцию/существующие тесты затронутого пути,
  race-проверку и независимый review по правилам проекта. Аналитический
  `CONCLUSIVE` доказывает избыточность wrapper, но не успешность будущей реализации.

**Итоговый scope:** pipeline_runtime.go, control_state.go, связанные сигнатуры
event_loop.go; event_loop_test.go, reader_channel_capacity_test.go,
sender_channel_capacity_test.go, read_batch_size_test.go, runtime_summary_test.go.
Новая Pause/Sender gate, удаление pool mutex/cond, изменение публичного контракта,
общая перестройка lifecycle и новый API pool в этот пункт не входят.

## 12. Pause живого Sender и фиксация отображаемых метрик

Статус: требования владельца приняты; реализация не выполнена.
Последующие уточнения владельца заменяют несовместимые выводы T8127 и прежнего
плана. Нужен обычный синхронизированный затвор, без специальной гарантии
физического сетевого старта в микроскопическом промежутке. Точный момент клика
и отдельный UI-протокол для этого не требуются.

### Выбранное поведение

- Sender pool и context живут весь run, включая Pause/Resume.
- Pause запрещает новые HTTP attempts, включая retries. Проверка затвора
  выполняется непосредственно перед следующим attempt.
- Выполняющийся HTTP request не отменяется и может завершиться success/failure.
  Success учитывается один раз. После failure тот же batch сохраняется;
  следующая попытка ждёт Resume.
- Resume будит ожидающих workers того же pool.
- Reader и throttler продолжают внутреннюю работу до backpressure. Файлы,
  частичные/удерживаемые batches, workers и очереди сохраняются.
- На Pause текущая отображаемая картинка метрик фиксируется до Resume/нового run.
  Если worker показан InFlightWorkers, он остаётся так показан, даже если
  физический HTTP уже завершился. Это снимок, а не live-состояние workers.
- Внутренний учёт реальных доставок продолжается и не теряет поздний success.
- Reset/fault/shutdown отменяют ожидания, ждут все стадии, затем освобождают очереди.

### Затвор и реакция backoff на Pause

В существующем senderPool добавить один resume channel под pool.mu.
Nil — работа разрешена; nonnil открытый канал — Pause. Pause создаёт его,
Resume закрывает и ставит nil. Повторные команды no-op по lifecycle.

Перед первой и каждой повторной попыткой под mutex проверить ctx и затвор.
При Pause ждать close канала либо ctx.Done; после пробуждения заново проверить
текущее состояние. Не выдавать заранее сохраняемый «допуск» на будущую попытку:
проверка находится у вызова attempt. При Resume → новой Pause старый wake
не отменяет проверку нового затвора. Mutex не держать на время HTTP response.

Backoff должен реагировать на Pause сразу, а не только по истечении долгого timer:

- Pause под pool.mu посылает существующим workers сигнал в их worker.wake.
- Ожидание retry принимает ctx.Done, timer и worker.wake. Wake приводит
  к проверке затвора; на Pause worker переходит в ожидание Resume.
- Сохранить deadline задержки и attempt number. Время backoff продолжает течь:
  после Resume ждать остаток, если deadline ещё впереди; иначе перейти
  к проверке перед attempt. Pause не сбрасывает jitter/delay и retry clock.
- Reconcile тоже использует wake: такой сигнал лишь перепроверяет состояние,
  не завершает задержку досрочно. Закрытие Resume будит всех gate waiters.
- Нынешний p.wait(ctx, delay) сам по себе не знает о Pause. Заменить его
  необходимым pool/worker-aware ожиданием; старую wait-подмену не сохранять
  как ограничение рабочего API.

Polling, global run-control, worker-local paused, pausePending,
acks и ожидание успешной доставки на Pause не добавлять.

### Владение batches и worker lifecycle

Intake может передать queued batch свободному worker на Pause. Worker остаётся
busy и удерживает batch до success или настоящей отмены; освобождение слота
раньше этого может перезаписать принятую работу. Новая очередь не нужна.
Число таких batches ограничено workers; сохраняются bounded очереди,
один held batch throttler и прежние локальные Reader buffers.

После failure backoff-state сохраняется до следующего attempt; gate wait
первого batch остаётся busy. Это внутреннее состояние; на Pause экран показывает
зафиксированный снимок. Новый публичный enum/field для gate wait не нужен.

Downscale сохраняет draining: busy worker заканчивает batch после Resume
и уходит по прежней границе. Reconcile не ждёт busy gate waiters; idle workers
уходят прежним wake-путём. Upscale/reanimation используют тот же pool/gate.
Настоящая отмена прерывает gate wait, backoff и HTTP.

### Конкретная фиксация метрик

Использовать уже существующий runtimeStatus как значение сохранённого снимка
в controlRunState. Новую runtimeSummary/paused-metrics wrapper не создавать.

- При обработке cmdPause закрыть затвор и зафиксировать текущие измерения:
  elapsed, totalTransactions, Reader/Throttler rates, worker categories,
  counters/depth/blocked durations обоих каналов. Сохранить копию значений.
- runtimeStatusAt в paused возвращает сохранённые измерения, а не перечитывает
  фактические pool/queue counters. Lifecycle, source error и изменяемые controls
  обслуживаются отдельно: команды не должны выглядеть неприменёнными из-за
  фиксации измерений. Не замораживать весь control-plane response.
- Поздний success продолжает попадать в внутренний atomic/накопительный учёт;
  metrics tick не меняет сохранённую картинку. actualTPS не обнулять на Pause
  и не заменять поздним значением: сохранить последнее показанное измерение.
- Накопительный учёт реальных доставок не откатывать к копии снимка. Не терять
  delta при Resume, Reset, fault или shutdown; накопительный Prometheus
  transactionsTotal сохраняет реальные success, даже когда экран зафиксирован.
- На Resume убрать снимок и начать новое окно rates от Resume. Накопленные
  успехи Pause включить в итоги один раз, не превращать весь paused interval
  в TPS одного короткого окна. Очереди/worker state после Resume показывать живыми.
- Reset очищает сохранённый снимок вместе с прогрессом после joins. Fault
  показывает faulted и диагностику по прежнему пути, снимая paused-картинку.
- Периодический runtime log/CLI status использует тот же runtimeStatus,
  поэтому paused-измерения не вычисляются заново при каждом чтении.

Фиксация в backend достаточна для текущего UI, который получает runtimeStatus
опросом. Специальная фиксация в момент клика и изменение JSON schema не нужны.
Frontend-изменения не включать без конкретного обнаруженного несовместимого пути.

### Команды и файлы

- cmdPause: вместо stopSender закрыть затвор, сохранить картинку и lifecycle.
  Не ждать завершения HTTP; event loop продолжает snapshot/settings/Resume/Reset/fault.
- cmdRun из paused: открыть затвор прежнего pool, снять картинку, продолжить elapsed.
  startSender выполнять только для нового run из idle.
- Reset из paused останавливает живой Sender через runtime.stop из пункта 10.
  Reset из running остаётся conflict. Stop отменяет ожидающих и ждёт joins.
- Только вместе с рабочим Sender gate удалить paused у controlState.throttlerSettings,
  поле settings.paused конкретной стадии throttler и pause-specific updates.
  Сохранить сам throttlerSettings, TPS/mode, installed && TPS==0, pacing,
  telemetry и ctx/updates. До этого этапа paused сохраняется.
- HTTP Pause сохраняет нынешний enqueue-only путь; новая receipt/schema не нужна.

Основной diff: sender_pool.go (затвор и retry wait), pipeline_runtime.go,
event_loop.go (команды, frozen runtimeStatus и metrics), control_state.go
(хранение снимка), throttler.go; соответствующие существующие тесты.
sender_http.go менять только если итоговый путь требует переноса проверки
к непосредственному attempt; классификация/доставка/закрытие body сохраняются.
Receiver, config и frontend в механизм не входят.

### Карта адаптации и необходимые проверки

| Нынешняя проверка | Итоговое поведение |
|---|---|
| PipelineRuntimeSenderInheritsRunContextAcrossPauseResume | Один pool/context внутри run; Pause не stop/recreate. App cancellation/Reset выполняют join. |
| SenderSnapshotKeepsAppliedControlsAcrossLifecycle | Applied controls продолжают работать; отображаемые worker categories на Pause неизменны, физический pool жив. Reset освобождает workers и snapshot. |
| PauseStopsConsumptionUntilRun | Проверять отсутствие новых HTTP attempts, не прекращение dequeue. Выполняющийся request завершается; следующая попытка того же batch ждёт Resume. |
| ResetFromPausedStopsReaderClearsProgressAndStartsFreshRun; ResetWhileZeroTPSHoldsBatchCompletes; source/startup fault | Сохранить joins, fault diagnostics, очистку старых batches и новые очереди. Включить отмену живого Sender на gate/backoff; ResetDuringRun остаётся conflict. |
| ThrottlerControlsApplyImmediatelyAndPersistThroughReset; SenderChannelTelemetryFollowsWindowPauseRunAndReset | Сохранить TPS/mode/persistence; различать меняющийся внутренний учёт и неизменные отображаемые rates/counters на Pause. |
| ReaderMeasurementsSurvivePauseAndClearOnReset; metrics-window/runtime-summary tests | Добавить snapshot-инвариант: повторные ticks/status reads не меняют paused-картинку; Resume показывает реальные итоги и новое rate window. |
| Actual-channel harness | Identity внутри Pause/Resume, capacity, batch-size telemetry, fresh queues/old-batch cleanup на Reset. Меж-run reuse не возвращать. |
| Sender retry/cancellation/backpressure/reconcile/downscale tests | При открытом затворе прежнее поведение. Pause во время длинного backoff немедленно переводит ожидание на gate, Resume сохраняет deadline/batch. |
| StopWaitsForAcceptedBatch; StopLeavesReadyBatchForResume | Сохранить stop/cancel/intake/join сценарии. Не выдавать mock, игнорирующий ctx, за доказательство доставки на Pause. Подмены оценить заново. |
| Throttler ControlUpdateEndsBlockedWaitWithoutAdmission | Вместо paused=true использовать installed/TPS=0. Сохранить завершение blocked measurement без отправки и тот же удерживаемый batch. Остальные pacing/zeroTPS/cancel/no-credit сценарии остаются. |
| HTTP commands/CLI/status; sender HTTP; Reader pools | Сохранить wire/command validation, диагностику, реальные resources/cancel. Форма прежних test seams не обязательна. |

Дополнить существующие сценарии проверками существенного нового поведения:

1. HTTP завершается success/failure после Pause; следующий retry не начинается.
   Тот же batch/ClientID доставляется после Resume, success учитывается один раз.
2. Несколько gate waiters просыпаются на Resume; повторная Pause перепроверяется.
   Длинный backoff быстро реагирует на Pause и сохраняет deadline.
3. Зафиксированный InFlightWorkers и rates/counters не меняются от завершения HTTP,
   Reader flow и metrics ticks. Внутренний success виден в итогах после Resume.
4. Reset/app cancel/fault освобождают gate/backoff waiters и ждут завершения.
   Downscale/upscale на Pause не теряет batch и не переиспользует busy slot.

Ожидания подтверждать управляемыми событиями и результатами, не произвольным sleep.
Нужную подмену сохранять только если сценарий нельзя разумно проверить через
реальные pool/HTTP/clock пути; не создавать production hooks ради старого теста.
Приёмка: Pause не ждёт request, запрещает следующие attempts, сохраняет данные,
фиксирует картинку; Resume продолжает run, учёт и rates без потерь/двойного счёта.
Выполнить затронутые Go-проверки и race по конкурентным путям после реализации.

## Оставшиеся решения

Обязательных нерешённых продуктовых вопросов для этих шагов нет.
Конкретный throttler и сохранение throttlerSettings включены в реализацию;
обязательный порядок не установлен.
Точность до миллисекунд и особые сетевые гарантии не добавлять. Если фактический
код выявит новую необходимость изменения wire/UI, показать конкретный путь
и вернуть владельцу именно этот вопрос, не отменяя его требований.

## Общие рекомендации разработчику и кодеру

- Перед каждым пунктом перечитать актуальные места вызова и существующий diff:
  LEAD может параллельно менять lifecycle. Чужие изменения сохранять.
- Удаление должно сокращать обязанности или дублирование. Новая упаковка параметров,
  интерфейс, manager, fallback или тестовая точка расширения требует доказанной
  нынешней необходимости; сама короткая сигнатура её не доказывает.
- Не сохранять элемент лишь потому, что на него написан тест. Определить требование,
  которое тест проверяет: удалённую деталь убрать, полезное поведение сохранить.
- Сначала адаптировать существующие проверки. Новые нужны только для существенного
  непокрытого поведения, а не для каждой новой формы кода. Не добавлять зависимости,
  новую тестовую инфраструктуру или production-синхронизацию ради refactor.
- При конкурентных проверках ждать конкретного перехода/результата. Произвольный
  sleep или отдельно взятые len(channel)/idle не доказывают завершение обработки.
- Начать с проверок затронутого пути, затем выполнить необходимые общие Go-проверки.
  Для изменений конкурентного кода нужна race-проверка. Не расширять suite и не
  повторять прогоны без новой правки, сбоя или неразрешённой причины.
- В выбранной реализации Receiver, frontend и HTTP/JSON/TOML schema не меняются.
  Новые требования владельца к Pause и отображению обязательны; если появится
  конкретная необходимость затронуть UI/контракт, назвать её отдельно.
  Сохранить batch до success/cancel, backpressure и порядок shutdown.

## Что сохраняем по результатам аудита

- Mutex/Cond, Sender intake boundary, busy/backoff/draining и уникальное владение
  Reader файлами: они обеспечивают текущие параллельную работу и остановку.
- Тестируемость отмены, retries, удержания batches и join. Удалённые подстановки
  Reader/throttler не восстанавливать; существующие attempt/wait сохранять только
  при доказанной необходимости после рефакторинга. «Бытие определяет тесты, а не наоборот»: инвентаризация
  нынешних seams не является требованием рабочего кода.
- Валидацию HTTP и прямых runtime commands, строгую CLI snapshot schema,
  timeout/context, проверку результата CLI set и прежние exit codes.
- Ограничения переполнения retry durations, учёт каждого blocked writer,
  очистку управляющих символов в status и накопительные Prometheus counters.
- Самостоятельные config/runtime контракты Receiver, валидацию программного
  Config, фиксирование выбранного response status и отказ от `[null]` до lab behavior.
- Используемые сейчас HTTP/Simulation adapters и защиту UI от устаревших async
  результатов. Полное ревью frontend остаётся отдельной работой; новых UI-кандидатов
  из непроверенных областей этот план не заявляет.

## 13. Аналитический аудит избыточных объёмов тестовых данных

Статус: анализ завершён в [T8144](../../../../ingestion-lab-agents-runtime/MAIL/ANALYST/OUT/T8144_20261003-2215_ANALYST_test-data-volume-audit_report.md),
охват 27/27 test-файлов, 176 Test-функций. Согласованное сокращение выполнено в
[T8146](../../../../ingestion-lab-agents-runtime/MAIL/CODER/OUT/T8146_20261003-2236_CODER_reader-ownership-cycles_report.md):
единственная замена 20 → 2; все assertions сохранены, адресный тест прошёл.
Отдельные full/race/vet/build не повторялись для изменения числа последовательных
циклов; результат принят владельцем, коммит 1b63b90.
Единственный малый кандидат — TestReaderPoolOwnsFilesAcrossConcurrentCycles:
20 → 2 последовательных цикла, 60 → 6 claims, без создания Transactions/файлов.
Два цикла сохраняют повторное владение и две одновременно удерживаемые работы.
Сокращение согласовано владельцем и реализовано отдельным этапом кодера;
значимого выигрыша времени не заявлено.
Для 100 000 строк busy-downscale минимальный достаточный объём не доказан;
oversized HTTP body нужен для проверки фиксированного лимита 32 МиБ.
Две неточные ссылки на строки в неизменяемом OUT оговорены в
[координационной записи](../../../../ingestion-lab-agents-runtime/ARCHIVE/PLANS/product-refactoring-reader-audit.md).

Поручить аналитику один проход по Go-тестам `loadgen/` и `receiver/`: найти
неоправданно большие batches, файлы, наборы строк и количество повторений,
которые не нужны для проверяемого поведения. Frontend не анализировать.

Для каждого кандидата указать тест и место создания данных, нынешний объём,
проверяемое требование, минимальный достаточный объём и обоснование сокращения.
Сохранить существенные границы: несколько batches, заполнение очереди,
backpressure, конкурентность и отличие нового значения от initial.
Большие значения в проверках валидации сами по себе не считать проблемой,
если они не приводят к созданию или обработке больших данных. Benchmark и
нагрузочные сценарии отделить от обычных функциональных тестов.

Стартовый кандидат — `TestReadBatchSizeIdleOnlyAndPersistsAfterReset` — уже
сокращён в принятом владельцем T8142: batch 2 000, файл 4 000 строк.
Коммит `210ec84`; адресный race `-count=20` и полный race suite прошли.
Повторно реализовывать это сокращение не нужно. Причина прежнего таймаута
остаётся недоказанной; объём данных не объявлять установленной причиной.

Результат — короткий список обоснованных сокращений либо вывод об отсутствии
кандидатов. Анализ без изменений кода; реализация отдельно после согласования.
Не добавлять тесты, подмены, зависимости или production hooks ради этого аудита.
