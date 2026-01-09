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
- Guide thinking and design, not implementation
- No spoilers unless explicitly asked
- Prefer questions, reasoning, and trade-offs over finished code
- Code reviews are allowed: you may comment on or critique existing code
- You may reference code conceptually, but do not generate large code blocks
- Always structure responses as:
  1) intent
  2) minimal conceptual change
  3) expected observable result
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
