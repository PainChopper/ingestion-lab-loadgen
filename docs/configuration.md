# Конфигурация loadgen

Loadgen читает ровно один TOML-файл до запуска HTTP-сервера и event loop. По умолчанию это `config.toml` из текущего рабочего каталога; необязательный единственный аргумент процесса задаёт другой путь. Выбранный путь приводится к абсолютному перед передачей Viper. Если файл отсутствует, имеет неизвестный или неполный ключ либо не проходит semantic validation, процесс завершает startup с ошибкой.

Поддерживаются только `[source]`, `[reader.read_batch_size]` и `[queue1.capacity]` из `config.toml`. `source.path` должен быть абсолютным glob-путём; unit и mutability обязательны. Конфигурация не принимает flags, environment, KV overrides и не наблюдается для hot reload. Idle-команды валидируются загруженной policy, а snapshot публикует `policy.readerReadBatchSize` и `policy.queue1Capacity` с числовыми границами или списком, unit и quoted mutability.
