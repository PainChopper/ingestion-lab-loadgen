# SEC2.4 — idle-only capacity readerChannel

Статус: завершено и принято.

## Цель

До запуска пользователь выбирает capacity первой канал. Выбор применяется к следующему Run; во время Run и Pause control недоступен. Изменение существующего канала на ходу не выполняется.

## Контракт

- Допустимые значения: `0, 1, 2, 4, 8, 16, …, 8192`.
- UI показывает равномерный дискретный slider по позициям шкалы, а не линейную шкалу capacity.
- `0` создаёт unbuffered канал; ненулевые значения создают buffered канал указанной capacity.
- Backend — источник applied value. Изменение разрешено только в `idle`; Run/Pause возвращают 409, невалидное значение — 400.
- Выбранное значение применяется при следующем запуске и сохраняется через Reset. Snapshot всегда показывает фактическую capacity текущей канал или выбранную capacity в idle.
- Вторая канал, Throttler, runtime reconfiguration и изменение capacity активного канала не входят.

## Выполненный маршрут

- Backend реализовал real capacity, HTTP validation и lifecycle; Go/race integration проверки прошли.
- Frontend подключил HTTP command и индексный cable; tests/build/lint прошли.
- Независимое Go-ревью нашло и закрыло MinInt validation defect; frontend review одобрило интеграцию.
- Browser smoke подтвердил `0`, `1`, `8192`, lifecycle capacity-control и Reset; процессы запускались скрыто и были остановлены.
