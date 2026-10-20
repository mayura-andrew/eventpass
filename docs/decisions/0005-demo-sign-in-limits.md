# 0005. Demo mode relaxes the sign-in rate limits

Status: accepted (2026-10-20)

## Context

Magic-link requests are limited per IP and per email (3 at once, 5 per 15 minutes). In demo mode, everyone who tries EventPass signs in as the same five demo accounts, and the end-to-end tests do so repeatedly from two browser profiles. The strict per-email limit then locks out the demo accounts, though demo mail never leaves the server: it goes to the in-memory catcher at `/dev/mail`.

## Decision

With `EVENTPASS_DEMO=1`, the magic-link and callback limits rise to 60 at once and 120 per window (per email per 15 minutes, per IP per minute). Registration and general per-IP API limits stay the same. The switch is an explicit server option (`RelaxedSignInLimits`), not tied to demo mode inside the API, so API tests keep checking the production limits.

## Consequences

- The public demo instance stays usable when many visitors share accounts.
- Tests don't depend on rate-limit timing. The production limits are covered by the Go API tests.
