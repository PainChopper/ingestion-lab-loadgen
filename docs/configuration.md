# Конфигурация loadgen

Поле `initial` в TOML и `config.<настройка>.initial` в JSON задаёт загруженное начальное значение настройки до runtime override. Команда меняет текущее применённое значение, сохраняя `config.initial`; для `startup-only` настроек начальное значение действует весь срок службы процесса. Это поле обязательно, включая допустимый ноль capacity. Старое имя `default` больше не принимается ни отдельно, ни вместе с `initial`; совместимого alias нет.

LoadGen читает один TOML-файл до запуска HTTP-сервера. При запуске без аргументов используется `config.toml` из текущего рабочего каталога. Другой файл указывается через `loadgen serve --config <path>`. Прогон стартует автоматически и выполняется один раз на процесс. Для штатной остановки нажми Ctrl+C или отправь SIGTERM.

Обязательное поле `http.addr` задаёт адрес прослушивания при запуске: непустой host и числовой порт 1–65535 в формате `host:port`; для IPv6 — `[::1]:8080`. Схема и путь здесь не указываются. Изменение требует перезапуска, DNS при проверке конфигурации не выполняется.

```toml
[http]
addr = "127.0.0.1:8080"
```

Если LoadGen слушает другой порт, например `127.0.0.1:8082`, передай его в remote CLI: `status --url http://127.0.0.1:8082`. Без флага CLI обращается к `http://127.0.0.1:8080`.

Поле `sender.api.url` задаёт адрес назначения Sender и проверяется при загрузке конфига. Нужен абсолютный HTTP(S) URL с хостом и без фрагмента. Sender использует указанный путь и query-параметры при отправке. В checked-in конфиге Sender отправляет батчи на `http://127.0.0.1:8081/internal/ingest`. Для локального запуска сначала запусти Receiver из каталога `receiver/` командой `go run ./cmd/receiver --config config.example.toml`, затем запусти LoadGen.

В схеме `schema_version = 3` поле `sender.api.timeout_ms` обязательно. Оно задаёт таймаут одной HTTP-попытки в миллисекундах, включая чтение ответа. Допустимо целое число от 1 до 9 223 372 036 854 включительно. В поставляемом конфиге задано 30 000 мс. Настройка применяется при запуске. Если попытка истекает по таймауту, Sender повторяет её по расписанию `sender.retry`.

Для чтения состояния есть CLI-команда `status`. JSON-снапшот доступен через HTTP `GET /api/loadgen/snapshot`. Во время прогона `set` принимает только `readers`, `senders`, `tps` и `throttle`. Все эти команды принимают необязательный `--url <url>`; по умолчанию используется `http://127.0.0.1:8080`. URL должен быть абсолютным HTTP(S) адресом без userinfo, query, fragment и базового пути.

Поддерживаются `[http]`, `[source]`, `[reader.read_batch_size]`, `[reader.workers]`, `[readerChannel.capacity]`, `[senderChannel.capacity]`, `[throttler.requested_tps]`, `[throttler.installed]`, `[sender.workers]`, `[sender.api]`, `[sender.retry]`, `[metrics.window_ms]` и `[logging]` из `config.toml`. `source.path` должен быть абсолютным glob-путём; unit и mutability обязательны. Конфигурация не принимает environment, KV overrides и не наблюдается для hot reload. Размер батча и ёмкости обеих очередей задаются при старте и действуют весь процесс. Для этих трёх настроек обязательно значение `startup-only` в TOML; оно же публикуется как `mutability` в JSON-снапшоте. Снапшот публикует настройки в поле `config`.

Настройка throttler использует bool: `[throttler.installed]`, `initial = true`, `allowed = [true, false]`, `mutability = "immediate"`. `true` включает ограничение TPS (при TPS=0 ожидает), `false` включает bypass, сохраняя запрошенный TPS. HTTP-команда: `{"action":"set-throttler-installed","value":false}`. CLI: `set throttle <true|false>`. Snapshot публикует `throttler.installed` и `config.throttlerInstalled` с булевыми initial/allowed; прежние строковые значения и имена не принимаются.
