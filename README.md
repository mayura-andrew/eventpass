# EventPass

Self-hosted event registration, QR passes and check-in that runs for free on a Google Sheet, a Gmail account and one small server.

> **Status: early development (milestone M0, foundation).** The demo runs end to end for sign-in and the control panel. Registration, passes, scanning, schedule and maps arrive in milestones M1–M3. See [PROGRESS.md](PROGRESS.md).

## Try the demo in one command

Needs Go 1.24+ and Node 20+.

```sh
(cd web && npm ci && npm run build) && EVENTPASS_DEMO=1 go run ./cmd/eventpass serve
```

Open <http://localhost:8080/admin/login>, pick a demo account, then open <http://localhost:8080/dev/mail> and click the sign-in link. Demo mode keeps everything in memory (two events, 150 participants) and catches every email instead of sending it.

| Demo account | Role |
| --- | --- |
| owner@example.org | Workspace owner |
| admin@example.org | Workspace admin |
| staff@example.org | Staff on DevFest Colombo 2026 |
| viewer@example.org | Viewer on DevFest Colombo 2026 |
| participant@example.org | Participant (portal at `/p/login`) |

## Development

```sh
go test ./cmd/... ./internal/... ./web/...      # Go unit, contract, API and authorization matrix tests
golangci-lint run ./cmd/... ./internal/... ./web/...
cd web && npm run lint && npm test              # web lint and unit tests
cd web && npm run build && npm run e2e          # Playwright against the demo
```

`api/openapi.yaml` is the source of truth for the API. After changing it, run `npm run gen:api` in `web/`; a Go test fails when routes, permissions or response shapes drift from the spec.

Run the Go server on :8080 and `npm run dev` in `web/` for hot reload; Vite proxies `/api` and `/dev`.

## Layout

```
cmd/eventpass/      CLI: serve, keygen, version
internal/domain/    every business rule, no I/O
internal/storage/   repository interfaces, contract tests, memory adapter
internal/auth/      magic links, encrypted sessions
internal/mail/      dev mail catcher and send queue
internal/api/       HTTP handlers; each route declares its permission
api/openapi.yaml    API source of truth
web/                React + TypeScript app, embedded into the binary
docs/               milestone plans and decision records
reference/          the prototype, read-only
```

## License

Apache-2.0. See [LICENSE](LICENSE).
