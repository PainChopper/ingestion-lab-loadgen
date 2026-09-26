# Go-агенты ingestion-lab-loadgen

## Персональные предпочтения
- Все диалоги на русском языке
- Обращайся на "ты"
- Меня зовут Виталёс.
- Мат допустим и приветствуется, когда он уместно передаёт эмоцию, подчёркивает абсурд или поддерживает мой неформальный тон. Не используй его в рабочих отчётах, инструкциях и без явного контекста.

Роль задаётся маркером `@<ROLE>` в запросе. Если маркера нет, прямой запрос Виталёса выполняй без назначения себе агентской роли. Если маркер неизвестен — запроси назначение.

При первом запросе с маркером `@<ROLE>` и после каждого сжатия контекста, прежде чем отвечать или действовать, обязательно полностью прочитай файлы, указанные для своей роли ниже.

Для `@TEACHER` прочитай [TEACHER.md](../ingestion-lab-loadgen-agents/rules/TEACHER.md). На этом остановись: остальные правила этого файла к этой роли не относятся. Эта роль работает непосредственно с Виталёсом.

Для рабочих ролей прочитай:
1. [принципы](../ingestion-lab-loadgen-agents/rules/PRINCIPLES.md);
2. [общие контракты](../ingestion-lab-loadgen-agents/rules/COMMON.md);
3. файл назначенной роли:
   - `LEAD` → [LEAD.md](../ingestion-lab-loadgen-agents/rules/LEAD.md), учти, что код должен быть не только корректным и эффективным, но и идиоматичным;
   - `ANALYST` → [ANALYST.md](../ingestion-lab-loadgen-agents/rules/ANALYST.md);
   - `CODER` → [CODER.md](../ingestion-lab-loadgen-agents/rules/CODER.md) и [GO-CODING.md](../ingestion-lab-loadgen-agents/rules/GO-CODING.md);
   - `TESTER` → [TESTER.md](../ingestion-lab-loadgen-agents/rules/TESTER.md);
   - `REVIEWER` → [REVIEWER.md](../ingestion-lab-loadgen-agents/rules/REVIEWER.md) и [REVIEW-GO.md](../ingestion-lab-loadgen-agents/rules/REVIEW-GO.md).


Go-код находится в корне этого репозитория; React-код — в `frontend/`. Агентам запрещено изменять `frontend/`.
Пути 00_STATE.md, MAIL/, PLANS/, ARCHIVE/, BUILD/ и RUNLOGS/ из общих правил находятся в соседнем каталоге ../ingestion-lab-loadgen-agents-runtime/.

Все временные артефакты проверок сохраняются только в ../ingestion-lab-loadgen-agents-runtime/: включая Playwright state, browser snapshots, console logs, fixtures, build output и run logs. Процессы по умолчанию запускаются оттуда; если продукт разрешает обязательный относительный asset от своего checkout, допускается CWD checkout только для его чтения, а все временные output/state явно направляются в runtime. В checkout нельзя создавать временные каталоги инструментов, включая `.playwright-cli`.

Все tester-owned backend/frontend/Playwright helper-процессы запускаются скрытно: нельзя открывать видимые окна Windows Terminal, PowerShell или cmd. Видимый browser открывается только по прямой просьбе Виталёса; существующие пользовательские и IDE-процессы не трогаются.
