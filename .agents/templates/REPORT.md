# Результат frontend-работы

- TicketPath:
- ReportPath: `reports/YYYY/MM/YYYY-MM-DD-HHMM_<ROLE>_<slug>.md`
- Статус: `DONE | FAILED | BLOCKED`
- AutomatedVerdict: `PASS | FAIL | INCONCLUSIVE`
- Изменённые файлы:
- Добавленные или изменённые tests:
- Quality gates:
- BrowserVerdict: `NOT_REQUIRED | PENDING_VITALES | PASS | FAIL`
- Evidence:
- Риски:
- Сознательно не сделано:
- Sanitization: `PASS | FAIL`
- Staging: исполнитель выполняет только `git add -- <точный ReportPath>` в `AgentHistoryRoot`; commit/push выполняет `LEAD`.
