// SPDX-License-Identifier: Apache-2.0
import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

type Caught = { id: string; to: string; text: string };

async function mailbox(page: Page): Promise<Caught[]> {
  const res = await page.request.get("/api/v1/dev/mail");
  return ((await res.json()) as { messages: Caught[] }).messages;
}

/**
 * Requests a magic link and follows it from the dev mailbox. Parallel tests
 * share the mailbox, so only a message that arrived after this request counts.
 */
export async function signIn(page: Page, email: string, audience: "staff" | "participant"): Promise<string> {
  const before = new Set((await mailbox(page)).map((m) => m.id));
  await page.goto(audience === "staff" ? "/admin/login" : "/p/login");
  await page.getByLabel("Email").fill(email);
  await page.getByRole("button", { name: "Email me a sign-in link" }).click();
  await expect(page.getByRole("heading", { name: "Check your inbox" })).toBeVisible();

  let link = "";
  await expect
    .poll(async () => {
      const mine = (await mailbox(page)).find((m) => m.to === email && !before.has(m.id));
      link = mine?.text.match(/https?:\/\/\S+\/login\/verify\?token=\S+/)?.[0] ?? "";
      return link;
    })
    .not.toBe("");
  await page.goto("/dev/mail");
  await page.locator(`a[href="${link}"]`).click();
  return link;
}

/** Fails the test on any WCAG 2.1 A/AA violation. */
export async function expectAccessible(page: Page) {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
}
