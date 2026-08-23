# Frontend-задача

- Цель:
- Основание agent delegation: `DIRECT_VITALES | TEACHER_CONFIRMED_VITALES`
- Bounded outcome:
- Acceptance criteria:
- AgentHistoryRoot: `../ingestion-lab-frontend-agent-system`
- TicketPath: `tickets/YYYY/MM/YYYY-MM-DD-HHMM_<ROLE>_<slug>.md`
- ReportPath: `reports/YYYY/MM/YYYY-MM-DD-HHMM_<ROLE>_<slug>.md`
- Принятое поведение:
- Scope:
- Вне scope:
- Обязательное чтение:
- Влияние на контракт:
- UI-инварианты:
- Проверки:
- Browser-сценарии:
- Владелец решения:
- Разрешение ticket commit/push:
- Разрешение completion commit/push:

## Batch / Reconciliation

- Batch: `NOT_REQUIRED | ACTIVE`
- Dispatch ledger ведётся для каждого dispatch независимо от роли, access mode и execution mode.

| DispatchId | Роль | Scope | AccessMode (`READ_ONLY | WRITE`) | ExecutionMode (`SEQUENTIAL | PARALLEL`) | ReportPath | Status | AutomatedVerdict |
|---|---|---|---|---|---|---|---|
| | | | | | | | |

- Все ожидаемые reports получены и классифицированы: `YES | NO | NOT_REQUIRED`
