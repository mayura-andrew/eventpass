# 0004. Pending magic-link tokens live in memory

Status: accepted (2026-10-20)

## Context

A magic link is valid for 15 minutes and used once. Storing its hash in a spreadsheet would spend Sheets quota on every login and put security material in a file organizers share.

## Decision

The server keeps `sha256(token) → {email, audience, expires}` in memory. v1 runs as a single instance (see the storage locks), so every request reaches the same map.

## Consequences

- A restart invalidates links sent in the last 15 minutes; people request a new one.
- No login data in the sheet. Login events are written to the audit log by user ID.
