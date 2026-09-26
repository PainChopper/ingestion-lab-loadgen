# Наследование context в Sender runtime

## Контекст

`main` создаёт application context через `signal.NotifyContext`. Он передаётся
в event loop и далее в `pipelineRuntime`.

Reader и Throttler создают дочерние context от context активного run. Sender
сейчас создаёт собственный context от `context.Background()`. Sender всё ещё
останавливается через явный `pool.stop()`, но его отмена не является частью
единого дерева context приложения и run.

## Цель

Сделать Sender дочерним участником context активного pipeline run.

## Решение

- `pipelineRuntime.startSender` передаёт context активного run в `startSenderPool`.
- `startSenderPool` принимает parent context и создаёт собственный отменяемый
  context через `context.WithCancel(parent)`.
- Явный `pool.stop()` сохраняется: он нужен для Pause и штатной остановки
  Sender до отмены Reader/Throttler.
- Отмена application context или context run должна также отменять Sender,
  включая intake и retry/backoff ожидание.

## Не менять

- HTTP API, control plane и snapshot schema.
- Retry policy, lossless retry и семантику Pause/Resume/Reset.
- Ownership очередей и graceful Reader downscale.
- Внутренний context Sender не должен становиться глобальным или переиспользоваться
  между run.

## Критерии приёмки

- Sender context наследуется от context конкретного pipeline run, а не от
  `context.Background()`.
- Application shutdown отменяет Reader, Throttler и Sender через одно дерево
  context.
- Pause по-прежнему сначала останавливает Sender через `pool.stop()` и не
  отменяет Reader/Throttler.
- Reset, runtime fault и final shutdown не оставляют Sender goroutine или
  retry/backoff ожидание.
- Есть regression tests на parent cancellation Sender и сохранение поведения
  Pause/Resume/Reset.
- UI/browser smoke подтверждает Run → Pause → Resume → Reset и shutdown без
  control-plane ошибок.
