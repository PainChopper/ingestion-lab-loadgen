# ingestion-lab-loadgen

`ingestion-lab-loadgen` — управляемая лаборатория нагрузки для экспериментов с ingestion pipeline. Она читает транзакции из Parquet, собирает их в batch-и, ограничивает поток в Throttler и передаёт дальше в Sender stage.

Проект нужен не для имитации «реалистичного» HTTP-клиента, а для воспроизводимого наблюдения за pipeline: состоянием запуска, скоростью, очередями, backpressure и применёнными настройками.

```text
Reader → Reader Channel → Throttler → Sender Channel → Sender
```

## Что уже работает

- lifecycle `idle` → `running` → `paused` с командами Run, Pause и Reset;
- чтение Parquet и сборка Reader batch-ей;
- две настраиваемые очереди между стадиями;
- batch-atomic Throttler с настройкой TPS и режимом `installed` / `bypass`;
- HTTP snapshot с реальными telemetry очередей и policy;
- React-лаборатория, которая подключается к backend через HTTP;
- Prometheus metrics и стандартные `pprof` endpoints.

## Быстрый запуск

Нужен Go 1.27 и Node.js для frontend.

```powershell
# из корня репозитория
go run .
```

По умолчанию процесс читает `config.toml`. Другой TOML-файл можно передать единственным аргументом:

```powershell
go run . D:\path\to\config.toml
```

Backend слушает только `127.0.0.1:8080`.

Для интерфейса в отдельном терминале:

```powershell
Set-Location frontend
npm install
npm run dev:backend
```

Vite проксирует `/api` на backend. Интерфейс доступен по адресу, который напечатает Vite; обычно это `http://localhost:5173/ingestion-lab-loadgen/`.

## Конфигурация

Конфигурация — один строго валидируемый TOML-файл. Неизвестные и отсутствующие поля завершают запуск с ошибкой; явные допустимые нули, например capacity `0`, разрешены.

| Раздел | Назначение | Когда меняется |
| --- | --- | --- |
| `[source]` | абсолютный glob-путь к Parquet | только до запуска |
| `[reader.read_batch_size]` | размер batch Reader | только в `idle` |
| `[readerChannel.capacity]` | capacity очереди Reader → Throttler | только в `idle` |
| `[senderChannel.capacity]` | capacity очереди Throttler → Sender | только в `idle` |
| `[throttler.requested_tps]` | запрошенный предел TPS | сразу |
| `[throttler.installation_mode]` | `installed` или `bypass` | сразу |

`0` для capacity означает небуферизованный Go channel. `0 TPS` в режиме `installed` удерживает batch; `bypass` пропускает ограничение. Throttler работает целыми batch-ами: перед отправкой batch ожидает расчётный интервал `размер batch / TPS`.

Актуальный пример находится в [config.toml](config.toml). Подробности формата и validation описаны в [docs/configuration.md](docs/configuration.md); этот документ пока требует синхронизации с полным набором текущих policy-разделов.

## HTTP API

### Snapshot

`GET /api/loadgen/snapshot` возвращает строгое дерево состояния:

```text
run
reader
throttler
sender
readerChannel
senderChannel
policy
```

В нём есть состояние запуска, применённые настройки, скорости, счётчики, заполненность и ожидания обеих очередей. Полный shape фиксируется тестами в [http_snapshot.go](http_snapshot.go).

### Команды

`POST /api/loadgen/commands` принимает JSON вида:

```json
{ "action": "set-requested-tps", "value": 2000 }
```

Поддерживаемые действия:

- `run`, `pause`, `reset`;
- `set-read-batch-size`;
- `set-reader-channel-capacity`;
- `set-sender-channel-capacity`;
- `set-requested-tps`;
- `set-throttler-installation-mode`.

Команды, изменяющие idle-only настройки во время Run или Pause, получают `409 Conflict`. Некорректные значения получают `400 Bad Request`.

## Наблюдаемость

- `GET /metrics` — Prometheus metrics;
- `/debug/pprof/` — Go pprof;
- frontend показывает тот же HTTP snapshot, а не собственную оценку backend telemetry.

## Текущие границы проекта

Sender stage пока потребляет batch-и внутри процесса; настоящая отправка в HTTP target — следующий крупный этап roadmap. Поэтому лаборатория уже измеряет и управляет pipeline до Sender, но ещё не является готовым нагрузочным HTTP-клиентом.

Настройки capacity применяются к следующему Run. Live resize уже работающего Go channel не реализован: обычный channel имеет неизменяемую capacity.

## Разработка

```powershell
# backend
go test ./...
go test -race ./...
go vet ./...
go build ./...

# frontend
Set-Location frontend
npm test
npm run lint
npm run build
```

Roadmap и принятые технические решения находятся в [docs/plans/roadmaps/backend-lab-integration-plan.md](docs/plans/roadmaps/backend-lab-integration-plan.md). Старые исследовательские отчёты могут описывать предыдущие этапы проекта и не заменяют этот README или текущий код.
