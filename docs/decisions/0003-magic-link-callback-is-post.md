# 0003. The magic-link callback is a POST from a page

Status: accepted (2026-10-20)

## Context

The brief lists `GET /auth/callback`. Corporate mail filters (Outlook Safe Links, Mimecast) open every link in an email to scan it. A GET that consumes a single-use token would sign the scanner in and leave the person with a dead link.

## Decision

The emailed link opens the web page `/login/verify?token=…`. That page sends `POST /api/v1/auth/callback` with the token in the body, then routes to `/admin` or `/p`. Link scanners fetch HTML but do not run the app's JavaScript, so the token survives.

## Consequences

- Tokens remain single use.
- The callback is a normal mutating request and gets the CSRF header check like every other one.
