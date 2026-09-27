# OpenAPI и Swagger UI для разработчиков

## Контекст

У loadgen уже есть HTTP API для snapshot и lifecycle-команд, но его контракт
описан только в коде и тестах. Разработчику приходится читать обработчики или
пользоваться отдельным UI, чтобы понять запросы, ответы и ошибки.

## Цель

Сделать текущий HTTP API понятным и интерактивным для разработки: хранить его
OpenAPI-описание в репозитории и открывать Swagger UI из запущенного loadgen.
UI должен позволять просматривать контракт и вручную вызывать существующие
endpoint'ы, включая lifecycle-команды и настройки.

## Границы

- Это developer-инструмент, а не новый публичный API и не второй control plane.
- OpenAPI фиксирует существующие paths, методы, JSON schema, status-коды и
  validation/error semantics; поведение endpoint'ов не меняется.
- Swagger UI вызывает только уже существующие HTTP handlers запущенного
  экземпляра loadgen.
- Не менять lifecycle, pipeline runtime, ownership очередей, frontend-продукт и
  transport между loadgen и будущим ingestion-lab-sink.

## Этапы

1. [ ] Сопоставить текущие HTTP handlers, request/response DTO, status-коды и
   contract tests; подготовить точное OpenAPI-описание существующего API.
2. [ ] Добавить выдачу OpenAPI-описания и developer-маршрут Swagger UI в
   запущенный loadgen, чтобы UI использовал тот же origin и мог вызывать
   существующие endpoint'ы через `Try it out`.
3. [ ] Добавить проверки против расхождения спецификации и HTTP-контракта:
   paths, методы, обязательные поля, ответы и ошибки должны оставаться
   согласованными при дальнейших изменениях API.
4. [ ] Провести независимое review и browser smoke: открыть Swagger UI,
   прочитать snapshot, выполнить `Run → Pause → Resume → Reset` и проверить,
   что результат совпадает с существующим UI и HTTP snapshot.

## Критерии приёмки

- В репозитории есть актуальное OpenAPI-описание текущего loadgen API.
- Запущенный service открывает Swagger UI по developer-маршруту.
- Swagger UI показывает status и команды без ручного составления JSON.
- `Try it out` вызывает существующие endpoint'ы и не создаёт второй путь
  управления lifecycle.
- Проверки обнаруживают несовпадение OpenAPI и реализованного HTTP-контракта.
- Browser smoke подтверждает жизненный цикл команд без control-plane ошибок.
