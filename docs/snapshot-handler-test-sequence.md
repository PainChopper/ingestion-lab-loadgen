# `TestSnapshotHandlerReturnsOwnerSnapshot` sequence

Тест проверяет обработку snapshot: синтетический GET непосредственно вызывает `snapshotHandler`, обработчик отправляет типизированный `snapshotRequest` владельцу состояния через общую очередь, а полученный `runtimeSnapshot` становится JSON-ответом.

```mermaid
sequenceDiagram
    participant Test as Test
    participant Handler as snapshotHandler
    participant Owner as Owner goroutine
    participant Recorder as ResponseRecorder

    Test->>Test: Create expected snapshot
    Test->>Owner: Start goroutine
    Note over Owner: Wait for snapshotRequest

    Test->>Handler: ServeHTTP(rec, req)
    Handler->>Owner: Send snapshotRequest through requests
    Note over Handler,Owner: Queue capacity is 1

    Owner->>Owner: Assert request type is snapshotRequest
    Owner-->>Handler: Send expected through request.reply
    Note over Handler,Owner: Reply channel capacity is 1

    Handler->>Recorder: Write application/json header
    Handler->>Recorder: Encode snapshot as JSON with implicit HTTP 200
    Handler-->>Test: Return from ServeHTTP

    Test->>Recorder: Read status, header, and body
    Test->>Test: Decode body into actual
    Test->>Test: Compare actual with expected
```

## Участники

- `req` — синтетический `GET /api/loadgen/snapshot`.
- `rec` записывает HTTP-ответ обработчика.
- Горутина владельца состояния заменяет реальный event loop и отвечает на `snapshotRequest`.
- `expected` — готовая структура snapshot, а не предварительно собранный JSON.
- `actual` декодируется из фактического тела HTTP-ответа и сравнивается с `expected`.

Обработчик ждёт ответ через буферный канал `request.reply`. В рабочем приложении отвечает event loop; здесь эту роль выполняет отдельная горутина. Проверки маршрутизации выполняются через ServeMux отдельно: неподдерживаемый метод даёт 405 с `Allow: GET, HEAD`, а реальный HTTP-сервер проверяет HEAD без тела в `TestServeMuxHEADUsesSnapshotAndReturnsNoBody`.

