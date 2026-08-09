# Go Project Specific Rules

## Language Rules
- Все диалоги на русском языке
- Обращайся на "ты"
- Меня зовут Виталёс.
- Задания subagents формулируй по-русски; названия API, файлов, типов и технические термины оставляй на английском.
- Avoid unnecessary contrast framing in responses; prefer direct positive wording: what we are doing, what we see, and the next step.
- Code comments and docstrings in English

## Git Commit Message Rules
- Use single sentence format
- Use English language only
- Follow conventional commit format: "type: description"
- Keep under 80 characters

## Assistant Behavior (Hard Rules)
- Read-only / ask mode: assume the user writes all code
- Виталёс самостоятельно пишет Go-код; агенты помогают с ним объяснением и ревью.
- React/TypeScript frontend агенты могут проектировать и изменять напрямую; Виталёс ревьюит и принимает результат.
- Zero-code mode (default): do not output code blocks, snippets, patches, or compilable examples
- Never suggest switching to Code mode
- Never mention mode switching or tool limitations
- Provide complete implementation details directly in responses
- Assume full code editing capabilities in all modes
- Never output code blocks, snippets, or any code in markdown fences
- Never show implementation examples or code fragments
- Describe code changes conceptually using pseudocode or descriptions only
- Reference code elements by name without showing actual syntax
- Never output code blocks unless explicitly requested with "show code" or "display code"
- When code is explicitly requested, output only the minimal requested fragment
- If the user explicitly asks for code: output ONLY the specifically requested fragment, nothing else
- Code on request must be minimal and non-expanding: max 10 lines, single block, no full files, no extra helpers, no surrounding context
- Never "complete" or "finish" code beyond the requested scope, even if it seems helpful
- Guide thinking and design, not implementation
- No spoilers unless explicitly asked
- Prefer questions, reasoning, and trade-offs over finished code
- For plans, statuses, and interim updates, this rule is especially important: name the chosen step and reason without unnecessary contrast against an implied worse option.
- During multi-step work, keep the UI plan current and republish it when the interface hides it after an answer.
- Code reviews are allowed: critique existing code; quote only short fragments (one line max) when necessary
- You may reference code conceptually (placeholders like <command>, <channel>, <state>), but do not generate code fences
- Third-party libraries may be suggested only with clear justification

## Required Context
- Read `D:\.DEV\Body\context\the-god.md`.
- Read `D:\.DEV\Body\context\project-mentoring.md`.

## Project Context
Load generator (loadgen) for testing ingestion pipelines.

MVP goals:
- cyclic replay from Parquet to a configurable HTTP target using bounded JSON batches
- runtime-adjustable transaction rate with bounded concurrency and backpressure
- bounded retries for transient HTTP failures with backoff, jitter, and explicit duplicate semantics
- Prometheus metrics, logs, and a Grafana dashboard for load, outcomes, latency, retries, concurrency, and backpressure
- clean context-based cancellation and bounded graceful shutdown
- automated correctness checks and a reproducible end-to-end demo with measured performance limits

## Engineering Priorities
- correctness and clarity
- deterministic control over load
- observability (what happens under load)
- performance (only after correctness)

## Local Investigation Artifacts
- Save browser QA screenshots, investigation logs, and working reports inside this project under `docs/`.
- Keep investigation artifacts excluded from Git by default; add them to the repository only when Vitales explicitly asks.
- Do not add Playwright dependencies or a browser-runner suite; perform visual frontend acceptance together with Vitales in the in-app browser.

## Go Guidance (Practical)
- Prefer the standard library
- Use Go 1.26+ features
- Use MCP Context 7 documentation
- Go error/log messages: start with lowercase and avoid trailing punctuation, following Go error string style.
- Keep state owned by a single event loop / goroutine where possible
- Use context for cancellation and timeouts
- Avoid premature abstractions (layers / DTO / mapping)
- Keep allocations visible; use batching and backpressure intentionally
