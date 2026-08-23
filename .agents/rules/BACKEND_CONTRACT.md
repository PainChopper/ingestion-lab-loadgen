# Граница frontend/backend контракта

## Источники истины

- Каноническая wire-спецификация: `ProductDocsRoot/frontend-backend-contract.md`.
- Зафиксированные расхождения и этапы интеграции: `ProductDocsRoot/backend-integration-report.md`.
- Frontend transport boundary: `FrontendRoot/src/adapters/LoadgenAdapter.ts`.

Layout-aware расположение и required-placement правило для product docs определены в [ProductDocsRoot](CONTROL_FLOW.md#productdocsroot-и-layout). Этот файл не дублирует wire schema. При изменении API, SSE, commands, receipts, capabilities или telemetry сверяйся с канонической спецификацией.

## Инварианты границы

- React-компоненты зависят от view model и `LoadgenAdapter`, а не от endpoints, SSE envelope или backend DTO.
- Wire DTO и React view model являются разными типами. Decode, validation и mapping выполняются на adapter boundary.
- `SimulationAdapter` — интерактивная песочница, а не источник wire-контракта или backend semantics.
- Неизвестная telemetry остаётся `null`/unknown и не превращается в синтетический `0`.
- Frontend-only `preview`, selection, geometry, CSS state и animation не входят в wire schema.
- Capabilities определяют support, writability, ranges, steps и apply modes. Компоненты не подставляют simulation-only пределы для реального backend.
- Snapshot и receipt применяются с учётом server instance, revisions и command identity; поздние данные не откатывают новое состояние.
- Команды одного регулятора сериализуются настолько, насколько требует канонический контракт.

## Contract scenarios

`SimulationAdapter`, будущий `FixtureAdapter` и будущий `HttpAdapter` должны проходить общий набор принятых сценариев там, где их capabilities совпадают:

- начальная загрузка полного состояния;
- applied, pending, noop и rejected command;
- защита от старых revisions и поздних receipts;
- reconnect/resync без повтора подтверждённой команды;
- capacity `0` с нулевой depth и возможным throughput;
- unknown telemetry и unavailable/read-only controls.

Adapter-specific тесты могут дополнять этот набор, но не переопределять общую product semantics.

## Конфликты

Если код, simulation и канонический документ расходятся, зафиксируй конкретный `GAP` с evidence. Не выбирай трактовку по догадке и не маскируй расхождение mapper-ом. Изменение принятого контракта требует явного решения Виталёса.
