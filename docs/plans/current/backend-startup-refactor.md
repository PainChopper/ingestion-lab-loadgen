# Backend startup refactor

## Цель

Упростить production wiring запуска load generator: `main` не должен содержать
анонимный адаптер для `startReaderPool`. Запуск pipeline остаётся под
ответственностью `controlState` и `eventLoop`.

## Наблюдаемая исходная конструкция

- `main` загружает policy, создаёт `controlState`, HTTP server и вызывает
  `eventLoop`.
- Перед вызовом `eventLoop` создаётся анонимная функция, которая связывает
  `startReaderPool` с `source.path` и telemetry, а затем преобразует результат
  в `readerRun`.
- `eventLoop` запускает Reader и Throttler при первом `Run`; затем запускает
  Sender pool. После `Pause` повторный `Run` возобновляет уже подготовленный
  pipeline и заново создаёт Sender pool.
- Параметры starter-ов нужны тестам для подмены реальных пулов.

## Целевое устройство

- Обычный production-вызов `eventLoop` не принимает Reader starter из `main`.
- `controlState` содержит именованную операцию запуска нового pipeline, которая
  использует policy и telemetry, уже находящиеся в состоянии.
- Эта операция запускает весь новый pipeline в правильном порядке: очереди,
  Reader, Throttler, Sender.
- Возобновление после `Pause` остаётся отдельным путём и не пересоздаёт Reader
  или Throttler.
- Тестовая подмена Reader и Throttler сохраняется в отдельном внутреннем
  test seam, а не в production wiring `main`.

## Инварианты поведения

- Первый `Run` создаёт обе очереди и запускает Reader, Throttler, затем Sender.
- `Pause` останавливает Sender, фиксирует telemetry и переводит Throttler в
  паузу, не останавливая Reader и Throttler.
- `Reset` и завершение процесса отменяют контексты, дожидаются пулов и
  отсоединяют telemetry очередей в прежнем порядке.
- Ошибка старта Reader возвращается вызывающей команде, очищает созданные
  очереди и остаётся видна в snapshot как `startError`.
- Семантика HTTP-команд, snapshot и метрик не меняется.

## План работы

1. Выделить из ветки `cmdRun` два явных сценария: старт нового pipeline и
   возобновление после паузы.
2. Перенести production-запуск Reader из анонимной функции `main` в
   именованную операцию `controlState`.
3. Оставить для unit-тестов внутреннюю точку подмены starter-ов; production
   entrypoint должен вызывать её с настоящими зависимостями.
4. Удалить анонимный адаптер из `main` и сделать его ответственность видимой
   через именованные методы состояния.
5. Обновить и запустить существующие unit-тесты lifecycle/event loop; отдельно
   проверить старт, pause/resume, reset и ошибку Reader startup.
6. Пройти под debugger первый `Run` и resume: подтвердить порядок стартов и
   отсутствие пересоздания Reader/Throttler после Pause.

## Готовность

- В `main` нет анонимной функции, адаптирующей `startReaderPool`.
- Точка запуска нового pipeline имеет имя, отражающее весь run, а не одну
  стадию.
- Существующие проверки поведения проходят без изменения внешнего контракта.
- В debugger подтверждены оба пути: первый `Run` и resume после `Pause`.
