// SPDX-License-Identifier: Apache-2.0

import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";

export type Schemas = components["schemas"];
export type User = Schemas["User"];
export type EventSummary = Schemas["Event"];
export type Participant = Schemas["Participant"];
export type Field = Schemas["Field"];
export type Checkpoint = Schemas["Checkpoint"];
export type ParticipantStatus = Schemas["ParticipantStatus"];
export type EventRole = Schemas["EventRole"];
export type Me = Schemas["Me"];

/** The server's one error shape, thrown by unwrap(). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown>;

  constructor(status: number, code: string, message: string, details?: Record<string, unknown>) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details ?? {};
  }

  /** Field errors from a validation_failed response: key → code. */
  fieldErrors(): Record<string, string> {
    const fields = this.details["fields"];
    const out: Record<string, string> = {};
    if (fields && typeof fields === "object") {
      for (const [k, v] of Object.entries(fields as Record<string, unknown>)) {
        if (typeof v === "string") out[k] = v;
        else if (v && typeof v === "object" && "code" in v) out[k] = String((v as { code: unknown }).code);
      }
    }
    return out;
  }
}

// Every mutating request carries the CSRF header the server requires.
const csrf: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD") {
      request.headers.set("X-EventPass-CSRF", "1");
    }
    return request;
  },
};

export const api = createClient<paths>({ baseUrl: "/api/v1", credentials: "same-origin" });
api.use(csrf);

type Result<T> = { data?: T; error?: unknown; response: Response };

/** Returns data or throws an ApiError, so TanStack Query sees failures. */
export async function unwrap<T>(p: Promise<Result<T>>): Promise<T> {
  let res: Result<T>;
  try {
    res = await p;
  } catch {
    throw new ApiError(0, "network", "Network error");
  }
  if (res.response.ok) return res.data as T;
  const body = res.error as
    { error?: { code?: string; message?: string; details?: Record<string, unknown> } } | undefined;
  throw new ApiError(
    res.response.status,
    body?.error?.code ?? "internal",
    body?.error?.message ?? res.response.statusText,
    body?.error?.details,
  );
}
