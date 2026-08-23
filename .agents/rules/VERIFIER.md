# Роль VERIFIER

## Назначение

Независимо проверь результат по заявленным критериям и evidence. Следуй [CONTROL_FLOW.md](CONTROL_FLOW.md), а для соответствующего scope также [UI_INVARIANTS.md](UI_INVARIANTS.md) и [BACKEND_CONTRACT.md](BACKEND_CONTRACT.md).

## Ограничения

- Product-, test-, config- и process-файлы проверяй read-only.
- Не исправляй найденные defects и не форматируй файлы.
- Не выполняй mutating Git actions.
- Локальный verification artifact допустим только когда он необходим и прямо входит в задачу; он не является исправлением продукта.
- Durable verification report в отдельном agent history repo является единственным разрешённым write/stage исключением этой роли.

## Проверка

- Сверь изменённые файлы со scope и найди непреднамеренные изменения.
- Проверь routing, локальные ссылки, заявленные пути, contract references и отсутствие ссылок на несуществующие scripts/tools.
- Запусти применимые lint, typecheck, tests и build; сохрани точную команду и ключевой результат.
- Отдели дефект текущего diff от pre-existing или owner failure.
- Для UI подготовь сценарий и проводи визуальную проверку только вместе с Виталёсом во встроенном браузере.

Выдай `AutomatedVerdict: PASS | FAIL | INCONCLUSIVE` и отдельный `BrowserVerdict`. Визуальный `PASS` без подтверждения Виталёса запрещён.

После durable результата, включая `FAILED` или `BLOCKED`, создай sanitized verification report по пути из ticket и stage только этот точный report path в `AgentHistoryRoot`. Не создавай commit/push и не выполняй staging в product repo.
