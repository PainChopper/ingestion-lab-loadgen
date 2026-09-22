# SEC2.2 — размер batch чтения

Статус: завершено и принято.

## Согласованные границы

- Настройку можно менять только в `idle`: это проверяют UI и backend. В running/paused команда возвращает HTTP 409.
- Сохранить default backend 50 000 строк. Диапазон существующего UI: от 1 000 до 100 000, шаг 1 000. Некорректный value — HTTP 400 без изменения состояния.
- Команда: POST `/api/loadgen/commands`, `{"action":"set-read-batch-size","value":25000}`. Успех — 2xx.
- Snapshot дополнительно содержит `readerReadBatchSize` — принятое backend значение. Оно используется следующим Reader и сохраняется после Pause/Run/Reset.
- HttpAdapter подключает существующее поле Read batch size, показывает подтверждённое backend значение; доступен ввод только при connected + idle, без отложенного применения в Pause.
- По уточнению владельца недоступные настройки должны быть визуально серыми и disabled: размер batch в Run/Pause и настройки capacity обеих канал. Показатели заполненности/ожидания в SEC2.3 остаются только для чтения.

## Маршрут и проверки

Backend CODER → frontend CODER → read-only REVIEWER → отдельные commit/push SEC2.2. TESTER только при необходимости для фронта. Go/integration tests выполняет backend-кодер; frontend tests/build/lint — frontend-кодер. Stdout/stderr/exit codes сохраняются сразу; LEAD и другие роли не повторяют прогоны.

Владелец настроил race detector на Windows (GCC, CGO_ENABLED=1, CC); итоговый integration-прогон кодера выполняется с `-race`.

Проверить границы value, запрет в running/paused, сохранение выбранного значения через Reset, фактический размер Reader batch, UI control и dispatch. Управление capacity и метрики канал не входят в этот подпункт.

## Выполненные проверки

- Backend-кодер: Go tests и integration с race detector прошли; проверены HTTP 400/409, сохранение конфигурации по Reset и реальный размер Reader batch.
- Frontend-кодер: 288 тестов, build и lint прошли. Серые disabled элементы и запрет позднего commit покрыты тестами.
- TESTER: browser сценарий 50 000 → 25 000 → Run → Pause → Reset прошёл; подтверждены applied-значение и серые disabled batch/readerChannel controls. Тесты кодеров не повторялись.
- REVIEWER выявил сохранение draft при отключении поля без blur. Узкое исправление NumericControl и stateful-parent регрессионный тест прошли; повторное read-only ревью одобрило итог.

SEC2.3 отложен по прямому указанию владельца и не запускается вместе с завершением этого подпункта.
