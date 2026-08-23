# Control flow frontend-работ

## Источник задачи и scope

- Прямая команда Виталёса задаёт цель, scope и принятые продуктовые решения.
- До изменения перечитай актуальные файлы и проверь текущее состояние worktree.
- Сохраняй незакоммиченные и параллельные изменения Виталёса. Не откатывай, не форматируй и не переписывай их ради удобства.
- Делай минимальный связный diff. Новые зависимости, инфраструктура, общие abstractions и соседние UX-решения требуют отдельного основания в запросе.
- Новые frontend-файлы, tests, fixtures и stories допустимы, когда прямо обслуживают принятое поведение.
- Отделяй `FACT` от `HYPOTHESIS`. Неподтверждённое не выдавай за состояние системы.

## Полномочия ролей

| Роль | Разрешённые изменения |
|---|---|
| `LEAD` | Tickets/reports и одобренные agent-rule changes в отдельном agent history repo; product code/tests не меняет. |
| `PAIDAGOGOS` | Файлы не меняет; ведёт primary user-facing plan/learning loop и использует только узкий read-only осмотр plan/state/code для объяснения. |
| `TEACHER` | Compatibility alias `PAIDAGOGOS`; полностью применяет его полномочия и ограничения без отдельного контракта. |
| `CODER` | Product-код frontend, связанные tests/stories и прямо заказанные process/config files в scope. |
| `TESTER` | Tests, fixtures и test helpers для уже принятого поведения; product behavior не меняет. |
| `VERIFIER` | Product- и process-файлы не меняет; собирает evidence и сообщает defects. |
| `ANALYST` | Работает read-only; описывает requirements, contracts, gaps и варианты. |

Git staging, commit, branch, push, merge и другие mutating Git actions выполняй только по прямой просьбе Виталёса и только в разрешённом repository.

`PAIDAGOGOS` и `LEAD` — sibling-роли с разным leadership. Основной маршрут `PAIDAGOGOS → Виталёс`; маршрут `PAIDAGOGOS → LEAD → CODER|TESTER|VERIFIER|ANALYST` открывается только после прямого решения Виталёса делегировать agent work. `PAIDAGOGOS` формулирует bounded outcome и acceptance criteria в разговоре, а ticket, dispatch, durable report, staging, commit, push и reconciliation полностью принадлежат `LEAD` и назначенным им ролям. `TEACHER` сохраняется только как compatibility alias `PAIDAGOGOS`.

## Внутренний dispatch субагентов

- `CODER`, `TESTER`, `VERIFIER`, `ANALYST` и другие исполнители запускаются только как внутренние субагенты текущей задачи через доступный `spawn_agent`. Namespace конкретной оболочки не является частью архитектурного контракта.
- Перед запуском `LEAD` сверяет фактическую схему инструмента. Для независимого чистого контекста используй `fork_turns: "none"`, если схема содержит `fork_turns`, либо точное семантически эквивалентное поле доступной оболочки, например `fork_context: false`. Не закрепляй единственный устаревающий синтаксис. Частичный контекст не является независимым; независимым `CODER` и `VERIFIER` передавай чистый контекст.
- `message` содержит полный bounded packet: роль, цель, scope, access mode, точные входные и выходные пути, входные факты, разрешённые и запрещённые изменения, acceptance criteria, обязательные проверки и формат результата.
- До spawn ledger row содержит stable unique `DispatchId`, unique sibling `TaskName`, ожидаемый `ReportPath` и `Status: PENDING`. `task_name` задаёт это внутреннее имя запуска, а не отдельную пользовательскую задачу.
- После successful spawn в той же row сохрани returned agent id либо canonical task name и `Status: DISPATCHED`. Durable row progression: `PENDING` → `DISPATCHED` → `TERMINAL_RESULT_RECEIVED` → reconciled actual status/verdict. При parallel launch после каждого successful spawn пересчитай `ActiveDispatches` в ledger order; failed spawn не добавляй.
- `model` и `reasoning_effort` необязательны и по умолчанию наследуются; они не являются версией, и долговечные имена моделей в контракте не фиксируются. При полном наследовании контекста override не задавай; override допустим только с чистым или ограниченным контекстом, если фактическая схема его поддерживает.
- `LEAD` ждёт terminal result через доступный wait-механизм, классифицирует `Status` и `AutomatedVerdict`, сохраняет report и выполняет reconciliation. Вопросы и блокировки исполнителя проходят только через `LEAD`.
- Перед spawn dependent implementation row проверь prerequisite rows текущего ticket. Для изменения STATE schema или single/parallel recovery semantics independent ANALYST обязан сначала закрыть exact schema outcome с verified `Status: DONE`, `AutomatedVerdict: PASS`, проверенным exact `ReportPath` и выполненной reconciliation. Любой non-pass, missing report, schema ambiguity или незавершённая reconciliation запрещает implementation dispatch; planned row остаётся `PENDING` и не входит в `ActiveDispatches`.
- Не требуй отсутствующую операцию close. Если конкретная схема предоставляет безопасный close/release, его можно вызвать только после reconciliation; interrupt не подменяет close/release.
- `create_thread`, `fork_thread` и другие отдельные видимые задачи или чаты запрещены без прямой просьбы Виталёса именно создать отдельную задачу или чат. Обычные формулировки «запусти агента», «передай кодеру» и «поручи верификатору» означают внутреннего субагента текущей задачи.
- Если внутренний запуск временно недоступен или достигнут лимит, `LEAD` ждёт освобождения capacity, переиспользует подходящего зарегистрированного исполнителя либо сообщает Виталёсу точный blocker.

`<GitTopLevel>/AGENTS.md`, `<GitTopLevel>/.agents/**` и `<GitTopLevel>/docs/agent-runs/**` являются local-only и не входят в product repository history. Не используй для них `git add -f`, `git add --force` или другой способ обхода ignore rules.

## Agent history и durable lifecycle

При работе внутри repository, содержащего `tickets/`, `reports/` и canonical `.agents/`, его Git root является `AgentHistoryRoot`; для history-only операций определять `FrontendRoot` не требуется. Перед product- или product-evidence операцией определи фактические `FrontendRoot` и владеющий им `GitTopLevel`. Текущий product использует embedded layout `<GitTopLevel>/frontend`; все frontend product paths в правилах трактуются относительно `FrontendRoot`.

Из product workspace `AgentHistoryRoot` вычисляется переносимо как sibling `../ingestion-lab-frontend-agent-system` относительно фактического `GitTopLevel`. Перед записью проверь, что это отдельный Git repository и что его canonical `.agents/**` соответствуют local-only product rules and templates; product-root `AGENTS.md` является локальным entrypoint. Не используй hardcoded пользовательский путь.

### Local STATE checkpoint

`<AgentHistoryRoot>/.agents-runtime/00_STATE.md` — единственная actual copy local-only checkpoint текущего `LEAD` lifecycle. Это не authoritative artifact, registry, queue или history: authority принадлежит tickets, reports и Git evidence. При расхождении `LEAD` rebuild-ит STATE из authoritative evidence и не изменяет ради согласования старые tickets, reports или Git history.

STATE содержит только девять scalar key/value lines: `LifecycleStatus`, `ActiveTicket`, `ActiveDispatches`, `ActiveStatus`, `NextAction`, `Blocker`, `LastLifecycleEvent`, `EvidenceRefs`, `UpdatedAt`. Допустимые значения `LifecycleStatus` ограничены `IDLE | ACTIVE | RECOVERING | BLOCKED`; update заменяет текущие значения и не добавляет headings, списки, таблицы, events или другую history. Только `LEAD` пишет checkpoint; исполнители по-прежнему пишут только свои reports.

`ActiveDispatches` содержит `NONE` либо 1–4 unique `DispatchId`, разделённые exact separator `, ` и расположенные в stable authoritative ticket-ledger order. В список входят только successfully launched и ещё не reconciled rows текущего active ticket: terminal id остаётся до reconciliation, а `PENDING`, never-launched и reconciled rows не входят. Ledger rows остаются authority для membership, runtime identity, per-dispatch status/verdict и expected `ReportPath`; STATE list является rebuildable checkpoint, runtime listing/wait — supporting evidence. `ActiveStatus` хранит только aggregate current phase: `DISPATCHED`, `PARTIAL_TERMINAL_RESULT_RECEIVED`, `TERMINAL_RESULT_RECEIVED`, `PARTIAL_RECONCILED` или `RECONCILED`.

| Lifecycle event | Обязательное изменение checkpoint |
|---|---|
| Ticket publication | Записать `TICKET_PUBLISHED`, `ACTIVE`, текущий ticket, следующее действие и ticket/Git evidence. |
| Dispatch | После каждого successful spawn добавить id в `ActiveDispatches` в ledger order, записать `DISPATCHED`, `ACTIVE`, `WAIT_FOR_ACTIVE_DISPATCHES` и ticket/runtime-ref evidence. |
| Partial terminal result | Сохранить terminal-unreconciled id в `ActiveDispatches`, записать `LastLifecycleEvent: TERMINAL_RESULT_RECEIVED` и aggregate `PARTIAL_TERMINAL_RESULT_RECEIVED`, проверить exact report, reconcile terminal row и продолжить ждать live rows. |
| Все outstanding results terminal | Сохранить ids до reconciliation, записать `LastLifecycleEvent: TERMINAL_RESULT_RECEIVED` и aggregate `TERMINAL_RESULT_RECEIVED`, затем reconcile каждый row независимо. |
| Partial reconciliation | Удалить только reconciled id, сохранить порядок остальных, записать `LastLifecycleEvent: RECONCILED`, aggregate `PARTIAL_RECONCILED` и ticket/report evidence. |
| Batch completion | После reconciliation всех launched rows записать `ActiveDispatches: NONE`, `ActiveStatus: RECONCILED` и `LastLifecycleEvent: RECONCILED`; lifecycle остаётся `ACTIVE` до доказанного final completion push. |
| Implementation commit или push | Записать соответственно `IMPLEMENTATION_COMMITTED` или `IMPLEMENTATION_PUSHED` и Git evidence; автоматически lifecycle не закрывать. |
| Completion commit или push | Записать соответственно `COMPLETION_COMMITTED` или `COMPLETION_PUSHED` и Git evidence; только после доказанного final push очистить active fields и blocker, установить `IDLE`. |
| `FAILED` | Записать `FAILED`; установить `RECOVERING` только для одного bounded recovery cycle с доказанной причиной и неизменным scope, иначе `BLOCKED`. |
| `BLOCKED` | Записать `BLOCKED`, один blocker и одно owner-facing следующее действие; прекратить dispatch. |
| Owner stop | Записать `OWNER_STOPPED`, `BLOCKED`, текущие evidence refs и прекратить dispatch. |
| State rebuild | Записать `LastLifecycleEvent: STATE_REBUILT`, `LifecycleStatus: RECOVERING` и transient `ActiveStatus: STATE_REBUILT`, затем evidence-derived lifecycle status и aggregate phase; historical artifacts не редактировать. |

## ProductDocsRoot и layout

`ProductDocsRoot` всегда находится внутри текущего product Git:

- embedded layout: `<GitTopLevel>/docs`;
- standalone layout: `<FrontendRoot>/docs`, что совпадает с `<GitTopLevel>/docs`.

Перед использованием product-документа проверь его наличие по требуемому относительному пути внутри `ProductDocsRoot`. При выделении standalone repository нужные документы должны быть перенесены в его собственный `docs/`; отсутствие обязательного документа является `GAP` или `BLOCKED`, а не основанием читать бывший parent repository. Product docs не копируются в `AgentHistoryRoot` и не входят в canonical scaffold.

Durable workflow:

1. `LEAD` создаёт ticket по [TASK template](../templates/TASK.md) в `tickets/YYYY/MM/YYYY-MM-DD-HHMM_LEAD_<slug>.md` либо с ролью назначенного исполнителя.
2. `LEAD` выполняет только `git add -- <точный-ticket-path>`, сверяет staged list, создаёт semantic ticket commit и push до передачи задачи исполнителю. Для push нужно явное разрешение текущего owner/task; без него handoff не выполняется как опубликованный workflow.
3. Исполнитель выполняет задачу в своём scope. После durable результата, включая `FAILED` или `BLOCKED`, создаёт concise report в `reports/YYYY/MM/YYYY-MM-DD-HHMM_<ROLE>_<slug>.md`.
4. Исполнитель stage-ит только собственный report командой `git add -- <точный-report-path>`, проверяет staged list и не создаёт commit/push.
5. `LEAD` проверяет report, evidence и sanitization, обновляет в точном `TicketPath` строку reconciled dispatch фактическими `Status` и `AutomatedVerdict` из точного `ReportPath`, затем выполняет только `git add -- <точный-TicketPath> <точный-ReportPath>` и проверяет, что staged inventory содержит ровно эти два пути.
6. `LEAD` делает отдельный semantic completion commit, включающий обновлённый ticket и соответствующий report, чтобы фактические status/verdict reconciled dispatch сохранялись в durable history, затем push при наличии разрешения. Один завершённый шаг соответствует одному completion commit/push.

Tickets/reports не копируются в product repo. В них допустимы только относительные product paths, краткое evidence и честные verdicts. Secrets, tokens, credentials, абсолютные локальные пути, персональный контекст, raw logs и screenshots запрещены; сырые артефакты остаются local-only в `docs/agent-runs/` product repo.

Во всех agent history операциях запрещены `git add .`, `git add -A`, `git add --all`, `git add -f`, `git add --force` и эквиваленты. Product repo никогда не stage/commit/push из этого workflow.

## Статусы и вердикты

Статус исполнения:

- `DONE` — scope выполнен, обязательные проверки завершены, известные ограничения названы;
- `FAILED` — результат не соответствует критерию и в текущем запуске не исправлен;
- `BLOCKED` — продолжение требует решения, доступа или внешнего состояния, которое агент не может получить сам.

`AutomatedVerdict`:

- `PASS` — проверяемые критерии подтверждены evidence;
- `FAIL` — найдено воспроизводимое несоответствие;
- `INCONCLUSIVE` — данных недостаточно для честного вывода.

Визуальный вердикт указывай отдельно:

- `NOT_REQUIRED` — видимое поведение не затронуто;
- `PENDING_VITALES` — automated checks завершены, совместная визуальная приёмка ещё не проведена;
- `PASS` — Виталёс подтвердил сценарий во встроенном браузере;
- `FAIL` — совместная проверка выявила дефект.

Агент не выставляет визуальный `PASS` самостоятельно.

## Quality gates

Для изменения product-кода или frontend-конфигурации обязательна последовательность:

1. `npm run lint`;
2. релевантные Vitest-тесты во время разработки;
3. полный `npm test` перед сдачей;
4. `npm run build`;
5. browser QA для видимого интерфейса или взаимодействия.

Текущий `npm run build` выполняет `tsc -b && vite build`: его первая стадия является обязательным typecheck. Отдельного npm script с именем `typecheck` сейчас нет, и требовать его нельзя.

Если Storybook установлен и находится в scope, дополнительно запускай его предусмотренную проектом build-проверку. Не придумывай отсутствующий script.

Для process-only Markdown достаточно проверить структуру, локальные ссылки и заявленные пути. Если менялись `package.json`, toolchain config или scripts, выполняй полный набор gates.

Падение общей проверки исследуй до конкретной причины. Не исправляй owner-изменения вне scope: отдели pre-existing failure от результата текущего diff и приведи команду и ключевой вывод.

## Evidence, журналы и результат

- Evidence — это актуальный код, документ, вывод команды, тест или совместно подтверждённый browser-сценарий.
- Не сохраняй credentials, tokens, полные чувствительные payloads и случайные terminal dumps.
- Только при необходимости сохраняй investigation logs и screenshots в `docs/agent-runs/<date>-<role>-<slug>/`. Каталог является локальным артефактом и не создаётся заранее.
- Итог должен быть коротким: статус, изменённые файлы, проверки, `AutomatedVerdict`, `BrowserVerdict`, риски и сознательно не выполненное.
- Durable report в agent history repo создаётся по lifecycle выше. Для него используй [шаблон результата](../templates/REPORT.md) или [шаблон верификации](../templates/VERIFY.md).
