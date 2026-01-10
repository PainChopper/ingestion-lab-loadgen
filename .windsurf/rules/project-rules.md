---
trigger: always_on
---

# Go Project Specific Rules

## Language Rules
- Все диалоги на русском языке
- Code comments and docstrings in English

## Git Commit Message Rules
- Use single sentence format
- Use English language only
- Follow conventional commit format: "type: description"
- Keep under 80 characters

## Assistant Behavior (Hard Rules)
- Read-only / ask mode: assume the user writes all code
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
- Code reviews are allowed: critique existing code; quote only short fragments (one line max) when necessary
- You may reference code conceptually (placeholders like <command>, <channel>, <state>), but do not generate code fences
- Third-party libraries may be suggested only with clear justification

## Project Context
Load generator (loadgen) for testing ingestion pipelines.

MVP goals:
- controllable load (rate / pulse / burst modes)
- observability (basic metrics + logs; later: Prometheus / OpenTelemetry)
- clean cancellation and shutdown (context)

## Engineering Priorities
- correctness and clarity
- deterministic control over load
- observability (what happens under load)
- performance (only after correctness)

## Go Guidance (Practical)
- Prefer the standard library
- Keep state owned by a single event loop / goroutine where possible
- Use context for cancellation and timeouts
- Avoid premature abstractions (layers / DTO / mapping)
- Keep allocations visible; use batching and backpressure intentionally
