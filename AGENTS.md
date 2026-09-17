# Go-агенты ingestion-lab-loadgen

Роль задаётся маркером `@<ROLE>` в запросе. Если маркера нет, прямой запрос Виталёса выполняй без назначения себе агентской роли. Если маркер неизвестен — запроси назначение.

При первом запросе с маркером рабочей роли и после каждого сжатия контекста, прежде чем отвечать или действовать, обязательно полностью прочитай:

1. [принципы](.agents/shared/rules/PRINCIPLES.md);
2. [общие контракты](.agents/shared/rules/COMMON.md);
3. файл назначенной роли:
   - `LEAD` → [LEAD.md](.agents/shared/rules/LEAD.md);
   - `ANALYST` → [ANALYST.md](.agents/shared/rules/ANALYST.md);
   - `CODER` → [CODER.md](.agents/shared/rules/CODER.md);
   - `TESTER` → [TESTER.md](.agents/shared/rules/TESTER.md);
   - `REVIEWER` → [REVIEWER.md](.agents/shared/rules/REVIEWER.md) и [REVIEW-GO.md](.agents/shared/rules/REVIEW-GO.md).

Для `@PAIDAGOGOS` и `@TEACHER` вместо рабочих правил прочитай [PAIDAGOGOS.md](.agents/rules/PAIDAGOGOS.md) и [личный контекст](../private-context/PAIDAGOGOS.private.md). Эти роли работают непосредственно с Виталёсом.

Go-код находится в корне этого репозитория; React-код — в `frontend/`. Go-агентам не поручено изменять `frontend/` без прямого указания в задаче.

Для Go-агентов пути `00_STATE.md`, `MAIL/`, `PLANS/`, `ARCHIVE/`, `BUILD/` и `RUNLOGS/` из общих правил находятся внутри `.agents/go-runtime/`. Фронтендовая агентская история в соседнем репозитории к этому состоянию не относится.

`LEAD` проверяет наличие нужных проектных документов и указывает их в `RequiredReads` по фактической задаче. Применимые установленные навыки указывай в `RequiredSkills`.
