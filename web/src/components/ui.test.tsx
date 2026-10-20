// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ApiError, type ParticipantStatus } from "../api/client";
import i18n from "../i18n";
import { ConfirmButton, ErrorAlert, StatusBadge, TextField } from "./ui";

describe("StatusBadge", () => {
  const statuses: ParticipantStatus[] = [
    "pending_approval",
    "waitlisted",
    "registered",
    "confirmed",
    "checked_in",
    "cancelled",
    "rejected",
  ];
  it.each(statuses)("shows %s with a text label and an icon, not color alone", (status) => {
    const { container } = render(<StatusBadge status={status} />);
    const badge = container.firstElementChild as HTMLElement;
    expect(badge.querySelector('[aria-hidden="true"]')?.textContent).toBeTruthy();
    expect(badge.textContent).toContain(i18n.t(`status.${status}`));
    expect(i18n.t(`status.${status}`)).not.toBe(`status.${status}`);
  });
  it("uses the translated label", () => {
    render(<StatusBadge status="checked_in" />);
    expect(screen.getByText("Checked in")).toBeInTheDocument();
  });
});

describe("TextField", () => {
  it("links the label, hint and translated error to the input", () => {
    render(<TextField label="Email" hint="Work email" value="" onChange={() => {}} error="invalid_email" />);
    const input = screen.getByLabelText("Email");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Work email Enter a valid email address.");
  });
});

describe("ErrorAlert", () => {
  it("translates error codes", () => {
    render(<ErrorAlert error={new ApiError(409, "last_owner", "server text")} />);
    expect(screen.getByRole("alert")).toHaveTextContent("The workspace needs at least one active owner.");
  });
  it("falls back to the server message for unknown codes", () => {
    render(<ErrorAlert error={new ApiError(400, "brand_new_code", "Server says no")} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Server says no");
  });
});

describe("ConfirmButton", () => {
  beforeAll(() => {
    // jsdom lacks <dialog> methods.
    HTMLDialogElement.prototype.showModal = function () {
      this.open = true;
    };
    HTMLDialogElement.prototype.close = function () {
      this.open = false;
    };
  });

  function Harness() {
    const [done, setDone] = useState(false);
    return (
      <>
        <ConfirmButton label="Remove" message="Really remove?" onConfirm={() => setDone(true)} />
        {done && <p>removed</p>}
      </>
    );
  }

  it("only acts after confirmation", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByText("Really remove?")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("removed")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove" }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(screen.getByText("removed")).toBeInTheDocument();
  });
});
