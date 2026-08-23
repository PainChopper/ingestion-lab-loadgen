# Верификация frontend

- TicketPath:
- ReportPath: `reports/YYYY/MM/YYYY-MM-DD-HHMM_VERIFIER_<slug>.md`
- Статус: `DONE | FAILED | BLOCKED`
- Объект проверки:
- Критерии:
- AutomatedVerdict: `PASS | FAIL | INCONCLUSIVE`
- BrowserVerdict: `NOT_REQUIRED | PENDING_VITALES | PASS | FAIL`
- Evidence:
- Gaps:
- Требуемые исправления:
- Pre-existing failures:
- Sanitization: `PASS | FAIL`
- Staging: `VERIFIER` выполняет только `git add -- <точный ReportPath>` в `AgentHistoryRoot`; commit/push выполняет `LEAD`.
