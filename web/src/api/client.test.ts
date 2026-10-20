// SPDX-License-Identifier: Apache-2.0
import { ApiError, unwrap } from "./client";

function res(status: number, body: unknown) {
  return { response: new Response(null, { status }), error: body, data: status < 300 ? body : undefined };
}

describe("unwrap", () => {
  it("returns data on success", async () => {
    await expect(unwrap(Promise.resolve(res(200, { ok: 1 })))).resolves.toEqual({ ok: 1 });
  });

  it("throws the server's error shape as ApiError with field errors", async () => {
    const body = {
      error: {
        code: "validation_failed",
        message: "x",
        details: { fields: { email: { code: "invalid_email" }, role: "invalid_option" } },
      },
    };
    const err = await unwrap(Promise.resolve(res(422, body))).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe("validation_failed");
    expect((err as ApiError).fieldErrors()).toEqual({ email: "invalid_email", role: "invalid_option" });
  });

  it("maps network failures to a network error", async () => {
    const err = await unwrap(Promise.reject(new TypeError("fetch failed"))).catch((e: unknown) => e);
    expect((err as ApiError).code).toBe("network");
  });
});
