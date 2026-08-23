# Правила агентов ingestion-lab

Этот локальный файл является единственной точкой входа agent system в корне product repository. Общие роли и шаблоны находятся в корневой `.agents/`. Отдельный agent-history repository хранит canonical scaffold, tickets, reports и scripts; product repository не хранит agent-history artifacts. Для frontend-работы `FrontendRoot` находится в `frontend/`. Прямая явно переопределяющая команда Виталёса имеет приоритет.

## Маршрутизация ролей

Для выбранного триггера обязательно прочитай и примени все файлы, указанные в соответствующей строке.

| Триггер | Обязательное чтение и применение |
|---|---|
| `@LEAD` | [.agents/rules/LEAD.md](.agents/rules/LEAD.md) |
| `@PAIDAGOGOS` | [.agents/rules/PAIDAGOGOS.md](.agents/rules/PAIDAGOGOS.md) и [../private-context/PAIDAGOGOS.private.md](../private-context/PAIDAGOGOS.private.md) |
| `@TEACHER` | [.agents/rules/PAIDAGOGOS.md](.agents/rules/PAIDAGOGOS.md) и [../private-context/PAIDAGOGOS.private.md](../private-context/PAIDAGOGOS.private.md) |
| `@CODER` | [.agents/rules/CODER.md](.agents/rules/CODER.md) |
| `@TESTER` | [.agents/rules/TESTER.md](.agents/rules/TESTER.md) |
| `@VERIFIER` | [.agents/rules/VERIFIER.md](.agents/rules/VERIFIER.md) |
| `@ANALYST` | [.agents/rules/ANALYST.md](.agents/rules/ANALYST.md) |

Прямое назначение роли словами `PAIDAGOGOS`, `ПЕДАГОГ` или `TEACHER` обязательно читает и применяет ту же пару файлов из строк `@PAIDAGOGOS` и `@TEACHER`. Прямое назначение роли словами `LEAD`, `CODER`, `TESTER`, `VERIFIER` или `ANALYST` обязательно читает и применяет соответствующий одиночный routed file из таблицы. Если роль не указана, прямой запрос Виталёса является достаточным основанием для работы по общим правилам. Не назначай себе роль по догадке.

## Границы системы

- Виталёс самостоятельно пишет Go-код; `PAIDAGOGOS` помогает объяснением, планом и ревью и применяет встроенный в свою роль полный Go pedagogical contract.
- React/TypeScript frontend агенты могут проектировать и изменять напрямую в пределах запроса; результат принимает Виталёс.
- `PAIDAGOGOS` ведёт обучение и следующий шаг, не пишет файлы и не выполняет Git mutations.
- `TEACHER` является коротким compatibility alias роли `PAIDAGOGOS`.
- `LEAD` обслуживает agent-history lifecycle и не пишет product code или tests.
- Не копируй персональный контекст, его пути или содержание в tickets, reports, prompts, Git history либо сообщения другим ролям.
- Не вводи дополнительные роли, очереди заданий или реестры состояния без прямой просьбы Виталёса.
