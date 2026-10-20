# Progress

Read this at the start of every session.

## Done

### M0 Foundation (plan: docs/progress/M0.md)

- Go module, cobra CLI (`serve`, `keygen`, `version`), env and `.env` config, graceful shutdown.
- Domain: workspace and event roles, permission map, participant statuses and transitions, field types with server-side validation, conditions and visibility, pass IDs, WCAG contrast check.
- Storage interfaces, a shared contract suite, the memory adapter and a deterministic demo seed (5 demo accounts, 2 events, 150 participants, checkpoint log consistent with statuses).
- Auth: magic links (hash only, 15 min, single use, uniform response, rate limited per IP and per email), AES-GCM session cookies with `SessionVersion` revocation, user vs participant sessions (ADR 0002).
- API: route table where each route declares its permission; one error shape; CSRF header; security headers (strict CSP, HSTS on https, camera only on `/scan`); `/dev/mail` catcher in demo mode.
- Authorization matrix test: 30 routes × 8 principals (anonymous, participant, owner, admin, event admin, staff, viewer, staff of another event), expectations written independently of the permission map.
- OpenAPI 3.1 spec with `x-permission` per operation; Go tests fail on route, permission or response-shape drift; generated TypeScript types with a CI freshness check.
- Web app (React, Vite, TanStack Query, i18next): staff and participant sign-in, verify page, control panel (events, event overview with live counts, participants with search/filter/edit, staff roles, users, workspace settings with contrast warning, audit log, system), portal (registrations), public event page, scanner placeholder. Code-split per area.
- Playwright e2e against the demo with axe checks; Vitest unit tests.
- CI: golangci-lint, Go tests with race detector, OpenAPI drift, govulncheck, web lint/typecheck/test/build, npm audit, Playwright.

## Next

- M1 Register and pass: Google Sheets adapter (cache, limiter, verify-before-write, migrations), setup wizard, SMTP transport and persisted outbox, public registration form, Ed25519 pass tokens, QR in email, PNG and PDF passes, `check`, `admin add`, `migrate`, `export` commands.

## Known issues

- Not yet verified in GitHub Actions: the workflow has only been exercised locally.
- The dashboard polls every 5 s; the SSE stream (`/events/{id}/stream`) lands with scanning in M2.
- `STORAGE=sheets` is not implemented yet, so only demo mode runs.
- Participant sessions can't be revoked per person (ADR 0002).
- The demo seed has no schedule or floor plan yet; those types arrive in M3.
