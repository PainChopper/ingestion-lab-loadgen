# UI-инварианты

## Владение состоянием

- Backend владеет lifecycle, pipeline state, capabilities, applied/pending configuration, command semantics и telemetry.
- React владеет selection, inspector, локальным `preview`, геометрией и визуальной анимацией между snapshot-ами.
- Компоненты не обращаются к transport напрямую и не воспроизводят backend execution model.

## Состояния controls

- `applied` — значение, которое backend использует сейчас.
- `pending` — принятое значение, которое ещё не вступило в силу.
- `preview` — локальное незавершённое редактирование до commit.
- `null`, измеренный `0`, `unsupported`, `readonly` и `unavailable` имеют разные смыслы и не подменяют друг друга.
- Запоздалая receipt или старый snapshot не откатывает более новое UI-состояние. Порядок определяется `serverInstanceId`, revisions и `commandId` согласно контракту.
- Commit, cancel, disabled и read-only semantics должны быть явными и одинаковыми для pointer и keyboard.

## Очереди, геометрия и flow-state

- Capacity `0` означает rendezvous: глубина и накопленные transactions равны нулю, throughput может быть ненулевым.
- Applied cable, pending/preview ghost, marker path и их endpoints используют одну согласованную геометрию.
- Изменения layout, SVG viewBox, coordinates, transforms, z-index и marker travel проверяются как единое визуальное поведение.
- Цвет, motion и pressure indication следуют принятой queue `flowState` specification. Компонент не выводит состояние из случайного локального порога.
- Визуальная скорость и число markers являются ограниченной проекцией telemetry, а не точным числом или скоростью реальных transactions.
- Responsive layout не должен скрывать controls, обрезать inspector или создавать неуправляемый overflow на согласованных viewport-ах.

## Accessibility

- Интерактивный control имеет доступное имя, keyboard path, видимый focus и корректные disabled/read-only состояния.
- Selection и inspector сохраняют предсказуемый focus order; закрытие не оставляет focus в удалённом элементе.
- Цвет и animation не являются единственным способом передать критическое состояние.
- Reduced motion сохраняет смысл и доступность сценария.

## Storybook

Storybook сейчас имеет статус `planned, not installed`. Условие старта и порядок зафиксированы в `ProductDocsRoot/frontend-storybook-implementation-plan.md`; layout-aware расположение и required-placement правило определены в [ProductDocsRoot](CONTROL_FLOW.md#productdocsroot-и-layout).

- Не устанавливай Storybook, dependencies, scripts, config или stories до принятого frontend baseline и отдельного разрешённого этапа.
- После старта этапа contract stories используют отдельный `FixtureAdapter`, детерминированные fixtures, frozen clock и reduced motion.
- Stories не используют live backend и не считают `SimulationAdapter` источником product или wire semantics.
- Story фиксирует значимое принятое состояние, а не все комбинации props.
- Stories и interaction tests дополняют Vitest и browser QA, но не заменяют их.

## Совместный browser QA

Визуальная приёмка выполняется вместе с Виталёсом во встроенном браузере. Для изменения видимого поведения подготовь короткий сценарий и доведи automated checks до завершения до совместной сессии.

Проверяй в затронутом scope:

- согласованный viewport и responsive boundaries;
- основной пользовательский сценарий и recovery;
- applied/pending/preview и command feedback;
- pointer, keyboard, focus, selection и inspector;
- SVG geometry, marker path, animation и z-order;
- console и network errors.

До подтверждения Виталёса указывай `BrowserVerdict: PENDING_VITALES`. Screenshots сохраняй только при необходимости по правилам [CONTROL_FLOW.md](CONTROL_FLOW.md).
