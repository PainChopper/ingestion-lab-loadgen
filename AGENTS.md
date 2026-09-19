# Go-агенты ingestion-lab-loadgen

Роль задаётся маркером `@<ROLE>` в запросе. Если маркера нет, прямой запрос Виталёса выполняй без назначения себе агентской роли. Если маркер неизвестен — запроси назначение.

При первом запросе с маркером `@<ROLE>` и после каждого сжатия контекста, прежде чем отвечать или действовать, обязательно полностью прочитай файлы, указанные для своей роли ниже.

Для `@TEACHER` прочитай [TEACHER.md](.agents/rules/TEACHER.md) и [личный контекст](../private-context/TEACHER.private.md). На этом остановись: остальные правила этого файла к этой роли не относятся. Эта роль работает непосредственно с Виталёсом.

Для рабочих ролей прочитай:
1. [принципы](../ingestion-lab-loadgen-agents/rules/PRINCIPLES.md);
2. [общие контракты](../ingestion-lab-loadgen-agents/rules/COMMON.md);
3. файл назначенной роли:
   - `LEAD` → [LEAD.md](../ingestion-lab-loadgen-agents/rules/LEAD.md), так же учти, что код должен быть не только корректным и эффективным, но и идиоматичным;
   - `ANALYST` → [ANALYST.md](../ingestion-lab-loadgen-agents/rules/ANALYST.md);
   - `CODER` → [CODER.md](../ingestion-lab-loadgen-agents/rules/CODER.md) и [GO-CODING.md](../ingestion-lab-loadgen-agents/rules/GO-CODING.md);
   - `TESTER` → [TESTER.md](../ingestion-lab-loadgen-agents/rules/TESTER.md);
   - `REVIEWER` → [REVIEWER.md](../ingestion-lab-loadgen-agents/rules/REVIEWER.md) и [REVIEW-GO.md](../ingestion-lab-loadgen-agents/rules/REVIEW-GO.md).


Go-код находится в корне этого репозитория; React-код — в `frontend/`. Агентам запрещено изменять `frontend/`.
Пути 00_STATE.md, MAIL/, PLANS/, ARCHIVE/, BUILD/ и RUNLOGS/ из общих правил находятся в соседнем каталоге ../ingestion-lab-loadgen-agents-runtime/.
