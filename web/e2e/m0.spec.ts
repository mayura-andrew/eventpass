// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "@playwright/test";
import { expectAccessible, signIn } from "./helpers";

test("owner signs in with a magic link and sees the seeded control panel", async ({ page }) => {
  await page.goto("/admin/login");
  await expect(page.getByRole("heading", { name: "Sign in to the control panel" })).toBeVisible();
  await expectAccessible(page);

  await signIn(page, "owner@example.org", "staff");
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.getByRole("heading", { name: "Events" })).toBeVisible();
  await expect(page.getByRole("link", { name: /DevFest Colombo 2026/ })).toBeVisible();
  await expectAccessible(page);

  await page.getByRole("link", { name: /DevFest Colombo 2026/ }).click();
  await expect(page.getByRole("heading", { name: "DevFest Colombo 2026" })).toBeVisible();
  await expect(page.getByText("Registered in total")).toBeVisible();
  await expectAccessible(page);

  await page.getByRole("link", { name: "Participants" }).click();
  await expect(page.getByText(/Showing 1–50 of 100/)).toBeVisible();
  await page.getByLabel("Search name, email or pass ID").fill("participant@example.org");
  await expect(page.getByRole("button", { name: "Pat Participant" })).toBeVisible();
  await expectAccessible(page);
});

test("a used magic link cannot be reused", async ({ page }) => {
  const link = await signIn(page, "admin@example.org", "staff");
  await expect(page.getByRole("heading", { name: "Events" })).toBeVisible();
  await page.context().clearCookies();
  await page.goto(link);
  await expect(page.getByRole("heading", { name: "Couldn't sign you in" })).toBeVisible();
});

test("staff only see the events they are granted, without admin tabs", async ({ page }) => {
  await signIn(page, "staff@example.org", "staff");
  await expect(page.getByRole("heading", { name: "Events" })).toBeVisible();
  await expect(page.getByRole("link", { name: /DevFest Colombo 2026/ })).toBeVisible();
  await expect(page.getByRole("link", { name: /Intro to Go Workshop/ })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Users" })).toHaveCount(0);
  await page.getByRole("link", { name: /DevFest Colombo 2026/ }).click();
  await expect(page.getByRole("link", { name: "Participants" })).toHaveCount(0);
});

test("a participant opens the portal and sees their registration", async ({ page }) => {
  await signIn(page, "participant@example.org", "participant");
  await expect(page).toHaveURL(/\/p$/);
  await expect(page.getByRole("heading", { name: "Your events" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "DevFest Colombo 2026" })).toBeVisible();
  await expect(page.getByText(/EP-[A-Z0-9]{4}-[A-Z0-9]{4}/)).toBeVisible();
  await expectAccessible(page);
});
