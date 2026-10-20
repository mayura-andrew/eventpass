# 0002. Participants are not rows in Users

Status: accepted (2026-10-20)

## Context

The system `Users` tab holds people who run events (owner, admins, staff, viewers). Participants are rows in each event's `Participants` tab, and email is their identity per event.

## Decision

Two kinds of session:

- **User session** (12 hours): for a row in `Users`. Carries `UserID` and `SessionVersion`; bumping the version signs the user out everywhere.
- **Participant session** (30 days): carries only the verified email. The portal looks up registrations by that email in each event.

The login page says which kind it wants (`audience: "staff"` from `/admin/login`, `"participant"` from `/p/login`). A staff member who also registered for an event signs in to the portal separately.

## Consequences

- `Users` stays small and readable; 1,000 participants never flood it.
- Participant sessions cannot be revoked per person in v1 other than by rotating `EVENTPASS_SECRET_KEY`. Cancelling or anonymising a registration removes what the session can see, which is what matters.
