# Конфигурация loadgen

Loadgen читает ровно один TOML-файл до запуска HTTP-сервера и event loop. По умолчанию это `config.toml` относительно текущего рабочего каталога; другой путь задаётся через `serve --config <path>`. Единственный позиционный путь остаётся устаревшим совместимым алиасом. `serve --run` запускает локальный сервис и сразу отправляет ему обычную lifecycle-команду Run через тот же control plane.

Remote lifecycle-команды не меняют конфигурацию: `snapshot`, `run`, `pause`, `resume`, `reset`. Каждая принимает необязательный `--url <url>`; по умолчанию используется `http://127.0.0.1:8080`. URL должен быть абсолютным `http`/`https` адресом без userinfo, query, fragment и base path. `resume` отправляет существующую команду `run`; новые HTTP endpoints и actions не добавляются.

Поддерживаются `[source]`, `[reader.read_batch_size]`, `[reader.workers]`, `[readerChannel.capacity]`, `[senderChannel.capacity]`, `[throttler.requested_tps]`, `[throttler.installation_mode]`, `[sender.workers]`, `[sender.api]`, `[sender.retry]`, `[metrics.window_ms]` и `[logging]` из `config.toml`. `source.path` должен быть абсолютным glob-путём; unit и mutability обязательны. Конфигурация не принимает environment, KV overrides и не наблюдается для hot reload. Idle-команды валидируются по загруженной конфигурации, а snapshot публикует её полный набор допустимых настроек в поле `config`.
