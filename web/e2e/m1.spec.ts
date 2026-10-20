// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "@playwright/test";
import { expectAccessible, mailbox, signIn } from "./helpers";

test("a participant registers, gets the pass email and opens the pass in the portal", async ({ page }, info) => {
  const email = `reg-${info.project.name}-${Date.now()}@example.org`;
  await page.goto("/e/devfest-2026");
  await expect(page.getByRole("heading", { name: "DevFest Colombo 2026" })).toBeVisible();
  await expect(page.getByText(/spots? left/)).toBeVisible();
  await expectAccessible(page);

  // Submitting empty marks the required fields and focuses the first one.
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.getByText("Some answers need fixing.")).toBeVisible();
  await expect(page.getByLabel(/^Full name/)).toBeFocused();
  await expectAccessible(page);

  await page.getByLabel(/^Full name/).fill("Playwright Tester");
  await page.getByLabel(/^Email/).fill(email);
  await page.getByLabel(/^College/).selectOption("SLIIT");
  await page.getByLabel(/^Year of study/).fill("2");
  await page.getByRole("radio", { name: "M", exact: true }).check();
  await expect(page.getByLabel(/^Dietary needs/)).toHaveCount(0);
  await page.getByLabel("I will have lunch").check();
  await page.getByLabel(/^Dietary needs/).fill("Vegetarian");
  await page.getByRole("button", { name: "Register" }).click();

  await expect(page.getByRole("heading", { name: "You're registered" })).toBeVisible();
  await expect(page.getByText(/spam/)).toBeVisible();
  await expectAccessible(page);

  await expect
    .poll(
      async () =>
        (await mailbox(page)).find((m) => m.to === email && m.subject.startsWith("Your pass"))?.attachments ?? [],
    )
    .toHaveLength(3);
  const passMail = (await mailbox(page)).find((m) => m.to === email && m.subject.startsWith("Your pass"));
  const types = passMail?.attachments?.map((a) => `${a.contentType}${a.inline ? " inline" : ""}`).sort();
  expect(types).toEqual(["application/pdf", "image/png", "image/png inline"]);

  // Registering again reveals nothing and sends a sign-in link instead.
  await page.goto("/e/devfest-2026");
  await page.getByLabel(/^Full name/).fill("Someone Else");
  await page.getByLabel(/^Email/).fill(email);
  await page.getByLabel(/^College/).selectOption("IIT");
  await page.getByLabel(/^Year of study/).fill("1");
  await page.getByRole("radio", { name: "S", exact: true }).check();
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.getByRole("heading", { name: "Check your inbox" })).toBeVisible();

  await signIn(page, email, "participant");
  await expect(page.getByRole("heading", { name: "DevFest Colombo 2026" })).toBeVisible();
  await page.getByRole("link", { name: "Open pass" }).click();
  await expect(page.getByRole("img", { name: /Pass for Playwright Tester/ })).toBeVisible();
  await expect(page.getByText(/EP-[A-Z0-9]{4}-[A-Z0-9]{4}/)).toBeVisible();
  await expectAccessible(page);

  for (const name of ["Download image", "Download PDF"]) {
    const href = await page.getByRole("link", { name }).getAttribute("href");
    const res = await page.request.get(href ?? "");
    expect(res.status()).toBe(200);
    expect(res.headers()["content-type"]).toMatch(name.endsWith("PDF") ? /application\/pdf/ : /image\/png/);
  }
});

test("an admin adds, previews, removes and restores a form field", async ({ page }, info) => {
  const label = `GitHub ${info.project.name} ${Date.now() % 100000}`;
  await signIn(page, "owner@example.org", "staff");
  await page.getByRole("link", { name: /Intro to Go Workshop/ }).click();
  await page.getByRole("link", { name: "Form", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Registration form" })).toBeVisible();
  await expectAccessible(page);

  await page.getByRole("button", { name: "Add field" }).click();
  await page.getByLabel("Label").fill(label);
  await expect(page.getByLabel("Key")).toHaveValue(/^github_/);
  await page.getByLabel("Type").selectOption("url");
  await page.getByLabel("Placeholder").fill("https://github.com/you");
  await expectAccessible(page);
  await page.getByRole("button", { name: "Save" }).click();

  const preview = page.getByRole("region", { name: "Preview" });
  await expect(page.getByRole("listitem").filter({ hasText: label })).toBeVisible();
  await expect(preview.getByLabel(label)).toBeVisible();
  await page.getByRole("button", { name: "Desktop", exact: true }).click();
  await expect(page.getByRole("button", { name: "Desktop", exact: true })).toHaveAttribute("aria-pressed", "true");

  await page.getByRole("listitem").filter({ hasText: label }).getByRole("button", { name: "Remove" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Remove field" }).click();
  await expect(preview.getByLabel(label)).toHaveCount(0);
  const removed = page.getByRole("region", { name: "Removed fields" });
  await removed.getByRole("listitem").filter({ hasText: label }).getByRole("button", { name: "Restore" }).click();
  await expect(preview.getByLabel(label)).toBeVisible();
});

test("an admin edits event settings", async ({ page }) => {
  await signIn(page, "admin@example.org", "staff");
  await page.getByRole("link", { name: /Intro to Go Workshop/ }).click();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Event settings" })).toBeVisible();
  await expectAccessible(page);
  await page.getByLabel("Venue name").fill("Lab 3");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Saved")).toBeVisible();
});
