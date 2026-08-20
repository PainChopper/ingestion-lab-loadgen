# Runtime reconfiguration

Обновлено: 2026-08-19

Статус: отложенное проектное решение. В текущий Backend MVP не входит.

## Назначение

Этот документ фиксирует внутреннюю backend-механику изменения работающего pipeline:

- динамическое увеличение и уменьшение worker pools;
- будущее изменение capacity bounded queues без reset процесса;
- границу между внутренним переходом и тем, что видит frontend.

Публичные команды, snapshot и telemetry остаются частью [frontend-backend-contract.md](frontend-backend-contract.md). Здесь описывается не второй контракт, а способ реализации его runtime-поведения.

## Порядок реализации

1. Сначала реализовать динамическое масштабирование reader и sender workers.
2. Увеличение worker count применять запуском новых workers.
3. Уменьшение выполнять мягко: лишний worker заканчивает текущую единицу работы и завершает goroutine.
4. Capacity обычных Go channels до отдельного этапа остаётся настройкой следующего run.
5. После готового pipeline отдельно реализовать live capacity через `ChannelRotator`.

## ChannelRotator

Buffered Go channel имеет неизменяемую capacity. Live resize моделируется сменой channel, а не изменением существующего объекта.

`ChannelRotator` управляет двумя физическими слотами:

- active channel принимает новые batch-и;
- retiring channel закрыт для новых отправок и дочитывается consumers.

Одновременно допускается только одно переключение. Следующий resize начинается лишь после того, как все consumers покинули retiring channel. Закрытый Go channel повторно открыть нельзя: при следующем переключении в освободившемся слоте создаётся новый channel object.

### Переключение producer-ов

1. `ChannelRotator` создаёт channel B с новой capacity.
2. Rotator просит всех producer-ов заменить локальный output A на B.
3. Producer подтверждает переключение только после того, как больше не способен выполнить send в A.
4. Невыданный локальный batch сохраняется producer-ом и после переключения отправляется уже в B.
5. Если producer может блокироваться на send, его цикл должен также принимать команду переключения или cancellation; resize не должен ждать освобождения полного A.
6. После подтверждения всех producer-ов rotator закрывает A.

Заполненность A не запрещает его закрытие. Buffered elements после `close(A)` продолжают выдаваться consumers с `ok == true`. Запрещён именно последующий send в закрытый channel, поэтому закрытие происходит только после producer acknowledgements.

Polling `len(A)` для этого не используется: мгновенная глубина buffer не доказывает, что producer-ы закончили отправку.

### Переключение consumer-ов

1. Каждый consumer хранит локальную ссылку на текущий input channel.
2. После закрытия A consumer продолжает получать оставшиеся buffered batch-и.
3. Когда A полностью исчерпан, receive возвращает zero value и `ok == false`.
4. Consumer получает у rotator текущий active channel B, заменяет локальную ссылку и продолжает работу.
5. Consumer подтверждает, что покинул A.
6. После подтверждения всех consumers старый слот можно освободить для следующего переключения.

Отдельный сигнал для начала перехода consumers не требуется: закрытие и последующее `ok == false` являются сигналом завершения старого поколения. Barrier обработки batch-ей между consumers не нужен; один consumer может уже работать с B, пока другой заканчивает ранее полученный batch из A.

### Инварианты

- Rotator является единственным владельцем смены active channel и закрытия retiring channel.
- Producer и consumer не закрывают channels и не изменяют общую active-ссылку.
- Producer не отправляет в A после подтверждения переключения.
- Одновременно существуют не более двух channel objects: active и retiring.
- Второе переключение не начинается до выхода всех consumers из retiring channel.
- Временная суммарная вместимость остатков A и нового B может превышать выбранную capacity; для лаборатории это допустимый переходный эффект.
- Когда rotator и все workers отпустили ссылки на старый channel, его освобождает Go GC.

## Наблюдаемое поведение

Frontend продолжает показывать одну логическую queue. Внутренние active/retiring channels, acknowledgements и процесс дренирования в UI не выводятся.

После переключения producer-ов новое значение становится applied capacity. Queue depth во время короткого перехода может включать остаток retiring channel и active channel и временно превышать applied capacity. Визуальное отношение `depth / capacity` остаётся ограниченным диапазоном `0..1`.

Точные названия API rotator-а (`Current`, `Switch` и acknowledgements) пока не фиксируются. Они выбираются при реализации вместе с моделью владения goroutine.

## Изменённые и конфликтующие постановки

### frontend-backend-contract.md

Текущая постановка говорит, что при уменьшении capacity ниже depth новое значение остаётся `pending`, backend прекращает допуск элементов сверх нового предела и применяет capacity после дренирования с `applyMode = after-drain`.

`ChannelRotator` меняет эту семантику:

- новый channel начинает принимать batch-и после producer acknowledgements;
- старый channel дренируется одновременно;
- новое значение считается applied после переключения producer-ов;
- отдельные pending/draining состояния внутренней ротации frontend не показывает.

Перед реализацией live resize соответствующий раздел контракта и HTTP command receipt необходимо привести к этой семантике. До этого текущий `after-drain` contract описывает существующую проектную постановку, а `ChannelRotator` остаётся отложенной заменой.

### batch-pipeline-decisions.md

Текущий документ закрепляет один batch channel и правило «producer owns closing the batch channel». Это остаётся верным для существующего fixed-capacity pipeline.

После появления live resize владение уточняется: producer подтверждает прекращение отправки, а retiring channel закрывает `ChannelRotator`, который координирует всех возможных producer-ов.

### loadgen-lab-approved-design.md

Текущий дизайн разрешает backend либо применить capacity после отпускания ручки, либо объявить настройку `next-run`/недоступной, если используется обычный неизменяемый Go channel.

На текущем этапе используется вариант `next-run`. После реализации `ChannelRotator` backend сможет применять capacity во время run, не показывая внутренние два channels в UI.

### queue-flow-state-spec-v0.1.md

Спецификация рассчитывает occupancy по одной applied capacity. При ротации логическая depth временно является суммой остатков retiring channel и active channel и может быть больше applied capacity. Существующее ограничение визуального pressure диапазоном `0..1` сохраняется; источник агрегированной depth потребуется уточнить при реализации telemetry.

### [Backend MVP plan](plans/current/backend-mvp-plan.md)

Противоречия нет. Текущий вертикальный срез намеренно не включает динамические workers и live queue resize. Настоящий документ подключён только как будущее направление после готового pipeline.
