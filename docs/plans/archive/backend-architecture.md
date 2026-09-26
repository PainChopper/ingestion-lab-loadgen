# Backend architecture roadmap

## Назначение

Этот план описывает долгосрочное устройство backend и не требует
реализовывать все этапы сразу.

## Наблюдаемая проблема

Сейчас `eventLoop` одновременно:

- управляет lifecycle load generator;
- обрабатывает команды и формирует snapshot;
- создаёт и останавливает конкретные стадии Reader, Throttler и Sender;
- хранит Reader-specific context, done и reconcile.

Это связывает control plane с Parquet Reader и HTTP-организацией файлов, хотя
HTTP — только один из способов управлять процессом.

## Целевой результат

Load generator — один управляемый сервис нагрузки, который:

- запускается через CLI с параметрами запуска;
- во время работы управляется и наблюдается через web-интерфейс;
- использует один control plane для CLI и web;
- не дублирует pipeline для разных способов управления.

## Целевые границы

### Control plane

Владеет lifecycle, типизированными командами, результатами команд и snapshot.
Не зависит от HTTP, JSON, CLI flags или конкретной стадии pipeline.

### Pipeline runtime

Владеет запуском, остановкой и координацией стадий одного run: очередей,
Reader, Throttler и Sender. Reader остаётся реализацией входной стадии, а не
деталью control plane.

### Adapters

HTTP adapter переводит HTTP/JSON в команды control plane и snapshot в HTTP
ответ. CLI adapter переводит аргументы и subcommands в те же команды и
представляет тот же результат пользователю.

### Composition root

`main` создаёт конфигурацию, control plane, pipeline runtime и выбранные
adapters, но не содержит деталей запуска конкретной стадии.

`main` также владеет application context, связанным с завершением процесса.
От него наследуются control plane, активный pipeline run и HTTP server.

## Этапы

1. [x] Упростить composition root: очистить `main`, выделить runtime metrics и
   сделать явными сценарии первого запуска и resume.
2. [x] Ввести application context и единый graceful shutdown: отмена процесса
   останавливает control plane и активный pipeline, а HTTP server прекращает
   работу штатно после завершения зависимых goroutine.
3. [ ] Вынести внутренние команды, результаты команд и snapshot из `http_*` файлов
   в независимый control-plane слой без изменения HTTP-контракта.
4. [ ] Выделить pipeline runtime как владельца ресурсов одного run; сократить
   Reader-specific поля и операции в `eventLoop`.
5. [ ] Перевести HTTP server на новый control plane и подтвердить неизменность
   существующего API.
6. [ ] Спроектировать и реализовать CLI adapter поверх того же control plane.
7. [ ] Провести узкий анализ общей lifecycle-логики Reader и Sender workers после
   выделения pipeline runtime. Выносить общий примитив только если совпадают
   владение ресурсами, масштабирование, shutdown и telemetry, а новая
   абстракция не требует type switches или stage-specific флагов.
8. [ ] В контрольной точке оценить, нужна ли следующая абстракция входной стадии
   после Reader/Parquet; не создавать её заранее.

## Инварианты

- Первый `Run`, `Pause`/resume, `Reset`, graceful shutdown и ошибка старта
  сохраняют наблюдаемую семантику.
- Один run владеет своими goroutine, context и очередями и завершает их
  детерминированно.
- Завершение приложения отменяет все дочерние runtime context и не оставляет
  работающих HTTP или worker goroutine.
- HTTP и CLI не создают собственные реализации lifecycle или pipeline.
- Внешний HTTP API не меняется случайно из-за внутреннего переноса типов.

## Контрольные вопросы перед каждым этапом

- Какая конкретная ответственность перемещается?
- Какой внешний контракт или сценарий поведения сохраняется?
- Уменьшилась ли связность, или появилась абстракция без второго потребителя?
