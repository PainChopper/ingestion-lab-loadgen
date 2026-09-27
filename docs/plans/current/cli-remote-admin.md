# Rich CLI и удалённое управление сервисом

## Контекст

`serve` запускает локальный load-generator service, но отдельный CLI-процесс не
может напрямую вызвать private control plane уже работающего процесса. Для
оператора нужны те же lifecycle-команды и наблюдение без обязательного открытия
web-интерфейса.

## Цель

Добавить полноценный CLI, который:

- умеет запускать локальный service;
- умеет сразу запускать нагрузку;
- умеет управлять и наблюдать уже запущенный service;
- не создаёт вторую реализацию lifecycle или pipeline.

## Решение

Есть два режима одного бинарника.

### Локальный service

```text
loadgen serve [--config FILE] [--run]
```

- `serve` запускает обычный HTTP service в `idle`;
- `serve --run` после успешного старта переводит тот же local control plane в
  `running` и продолжает обслуживать HTTP/UI;
- `--config` выбирает существующий TOML-файл; новый формат конфигурации не
  вводится.

### Remote-admin client

```text
loadgen snapshot [--url URL]
loadgen run      [--url URL]
loadgen pause    [--url URL]
loadgen resume   [--url URL]
loadgen reset    [--url URL]
loadgen set reader-workers N [--url URL]
loadgen set sender-workers N [--url URL]
loadgen set requested-tps N [--url URL]
loadgen set valve-mode installed|bypassed [--url URL]
```

- remote-admin использует существующие `/api/loadgen/snapshot` и
  `/api/loadgen/commands` как HTTP client adapter;
- он не подключается к private control plane другого процесса и не создаёт
  собственный pipeline;
- `resume` отправляет ту же lifecycle-команду, что `run` из `paused`;
- `--url` имеет единый documented default (определяется при реализации) и
  валидируется до сетевого запроса.

## Формат вывода

- успешный mutating command печатает краткий итог: действие и итоговый state;
- `snapshot` печатает стабильный машиночитаемый JSON по wire schema сервиса;
- ошибки сети, HTTP error response и malformed snapshot печатаются в stderr с
  ненулевым exit code;
- CLI не печатает retry policy, секреты, request body или скрытые config values.

## Не менять

- private control plane, pipeline runtime и ownership очередей;
- HTTP URL, методы, JSON schema, status-коды и текущую UI-семантику;
- retry policy, lossless retry, graceful Reader downscale и Sender context;
- frontend и Storybook;
- не вводить shell/REPL, daemon protocol, authentication или новый RPC.

## Этапы

1. [ ] Проанализировать текущий CLI/startup path и точный HTTP command/snapshot
   contract; принять default URL и exit-code matrix.
2. [ ] Реализовать `serve [--config] [--run]` через существующий composition
   root и local control plane без дублирования server lifecycle.
3. [ ] Реализовать remote-admin HTTP client adapter и subcommands `snapshot`,
   `run`, `pause`, `resume`, `reset`.
4. [ ] Добавить `set` subcommands для уже существующих controls с той же
   validation/error semantics, что у HTTP adapter.
5. [ ] Добавить unit/HTTP contract tests, независимый review и CLI smoke against
   a real local service; отдельно UI/browser smoke `serve --run`.

## Критерии приёмки

- `serve` стартует service в `idle`, а `serve --run` — в `running`.
- Remote commands изменяют тот же lifecycle, который виден в UI и snapshot.
- `snapshot` CLI и HTTP snapshot совпадают по JSON contract.
- Remote `run → pause → resume → reset` сохраняет текущие state/status semantics.
- `set` commands не обходят HTTP validation и не создают второй control path.
- Сетевые/HTTP/validation ошибки имеют понятный stderr и ненулевой exit code.
- CLI smoke и UI/browser smoke проходят без control-plane ошибок.
