# Конфигурация loadgen

Поле `initial` в TOML и `config.<настройка>.initial` в JSON задаёт загруженное начальное значение настройки до runtime override. Команда меняет текущее применённое значение, сохраняя `config.initial`; для `startup-only` настроек начальное значение действует весь срок службы процесса. Это поле обязательно, включая допустимый ноль capacity. Старое имя `default` больше не принимается ни отдельно, ни вместе с `initial`; совместимого alias нет.

Loadgen читает ровно один TOML-файл до запуска HTTP-сервера и event loop. По умолчанию это `config.toml` относительно текущего рабочего каталога; другой путь задаётся через `serve --config <path>`. Единственный позиционный путь остаётся устаревшим совместимым алиасом. `serve --run` запускает локальный сервис и сразу отправляет ему обычную lifecycle-команду Run через тот же control plane.

Обязательное поле `http.addr` задаёт адрес прослушивания при запуске: непустой host и числовой порт 1–65535 в формате `host:port`; для IPv6 — `[::1]:8080`. Схема и путь здесь не указываются. Изменение требует перезапуска, DNS при проверке конфигурации не выполняется.

```toml
[http]
addr = "127.0.0.1:8080"
```

Для сервера с `addr = "127.0.0.1:8081"` remote CLI вызывается отдельно: `snapshot --url http://127.0.0.1:8081`. Его URL по умолчанию остаётся `http://127.0.0.1:8080` и не читается из серверного конфига. `sender.api.url` также независим: для нагрузки на встроенный ресивер этого сервера явно задай `http://127.0.0.1:8081/internal/test/ingest`.

Путь встроенного ресивера берётся из `sender.api.url` при запуске. Например, `http://127.0.0.1:8081/test/receive?source=loadgen` регистрирует `/test/receive`; Sender отправляет полный URL с query-параметрами. URL без пути регистрирует `/`. Ошибка разбора URL или конфликт со служебным API, метриками, developer/pprof маршрутом останавливает запуск с ошибкой `sender.api.url`. Host и порт назначения Sender не определяют `http.addr` автоматически; для локального стенда они должны вести к доступному адресу сервера.

Remote lifecycle-команды не меняют конфигурацию: `snapshot`, `run`, `pause`, `resume`, `reset`. Каждая принимает необязательный `--url <url>`; по умолчанию используется `http://127.0.0.1:8080`. URL должен быть абсолютным `http`/`https` адресом без userinfo, query, fragment и base path. `resume` отправляет существующую команду `run`; новые HTTP endpoints и actions не добавляются.

Поддерживаются `[http]`, `[source]`, `[reader.read_batch_size]`, `[reader.workers]`, `[readerChannel.capacity]`, `[senderChannel.capacity]`, `[throttler.requested_tps]`, `[throttler.installed]`, `[sender.workers]`, `[sender.api]`, `[sender.retry]`, `[metrics.window_ms]` и `[logging]` из `config.toml`. `source.path` должен быть абсолютным glob-путём; unit и mutability обязательны. Конфигурация не принимает environment, KV overrides и не наблюдается для hot reload. Idle-команды валидируются по загруженной конфигурации, а snapshot публикует её полный набор допустимых настроек в поле `config`.

Настройка throttler использует bool: `[throttler.installed]`, `initial = true`, `allowed = [true, false]`, `mutability = "immediate"`. `true` включает ограничение TPS (при TPS=0 ожидает), `false` включает bypass, сохраняя запрошенный TPS. HTTP-команда: `{"action":"set-throttler-installed","value":false}`. CLI: `set throttler-installed <true|false>`. Snapshot публикует `throttler.installed` и `config.throttlerInstalled` с булевыми initial/allowed; прежние строковые значения и имена не принимаются.
