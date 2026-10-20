// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import type { Field } from "../api/client";
import { keyFromLabel } from "../areas/admin/FormPage";
import { emailFromKey } from "../areas/admin/SetupWizard";
import i18n from "../i18n";
import { conditionMet, formFields, missingRequired, ProfileForm } from "./ProfileForm";

let order = 0;
function field(p: Partial<Field> & Pick<Field, "key" | "type">): Field {
  return {
    label: p.key,
    required: false,
    validation: {},
    editable: "always",
    visibility: "staff",
    order: ++order,
    hidden: false,
    builtin: false,
    ...p,
  };
}

const allTypes: Field["type"][] = [
  "short_text",
  "long_text",
  "email",
  "phone",
  "number",
  "date",
  "single_select",
  "multi_select",
  "checkbox",
  "consent",
  "url",
  "photo",
  "file",
  "section",
];

function Harness({ fields, initial = {} }: { fields: Field[]; initial?: Record<string, string> }) {
  const [values, setValues] = useState(initial);
  return (
    <>
      <ProfileForm fields={fields} values={values} onChange={(k, v) => setValues((c) => ({ ...c, [k]: v }))} />
      <output data-testid="values">{JSON.stringify(values)}</output>
    </>
  );
}

const values = () => JSON.parse(screen.getByTestId("values").textContent ?? "{}") as Record<string, string>;

describe("ProfileForm", () => {
  it.each(allTypes)("renders a %s field with its label", (type) => {
    render(<Harness fields={[field({ key: "f", type, label: `Label ${type}`, options: ["A", "B"] })]} />);
    expect(screen.getAllByText(`Label ${type}`).length).toBeGreaterThan(0);
    expect(i18n.t(`fieldTypes.${type}`)).not.toBe(`fieldTypes.${type}`);
  });

  it("labels every input so screen readers announce it", () => {
    const fields = allTypes
      .filter((t) => !["section", "photo", "file", "single_select", "multi_select"].includes(t))
      .map((type) => field({ key: type, type, label: `L-${type}` }));
    render(<Harness fields={fields} />);
    for (const f of fields) expect(screen.getByLabelText(new RegExp(`^L-${f.type}`))).toBeInTheDocument();
  });

  it("stores checkboxes as TRUE or empty, like the sheet", async () => {
    render(<Harness fields={[field({ key: "lunch", type: "checkbox", label: "Lunch" })]} />);
    await userEvent.click(screen.getByLabelText("Lunch"));
    expect(values()["lunch"]).toBe("TRUE");
    await userEvent.click(screen.getByLabelText("Lunch"));
    expect(values()["lunch"]).toBe("");
  });

  it("joins multi-select values in option order", async () => {
    render(<Harness fields={[field({ key: "langs", type: "multi_select", options: ["Go", "Python", "Rust"] })]} />);
    await userEvent.click(screen.getByLabelText("Rust"));
    await userEvent.click(screen.getByLabelText("Go"));
    expect(values()["langs"]).toBe("Go; Rust");
  });

  it("uses radios for short single-choice lists and a select for long ones", () => {
    render(
      <Harness
        fields={[
          field({ key: "size", type: "single_select", options: ["S", "M", "L"] }),
          field({ key: "year", type: "single_select", label: "Year", options: ["1", "2", "3", "4", "5"] }),
        ]}
      />,
    );
    expect(screen.getAllByRole("radio")).toHaveLength(3);
    expect(screen.getByRole("combobox", { name: "Year" })).toBeInTheDocument();
  });

  it("shows a conditional field only when its condition is met", async () => {
    const fields = [
      field({ key: "lunch", type: "checkbox", label: "Lunch" }),
      field({ key: "diet", type: "short_text", label: "Dietary needs", condition: { field: "lunch" } }),
    ];
    render(<Harness fields={fields} />);
    expect(screen.queryByLabelText("Dietary needs")).not.toBeInTheDocument();
    await userEvent.click(screen.getByLabelText("Lunch"));
    expect(screen.getByLabelText("Dietary needs")).toBeInTheDocument();
  });

  it("never shows hidden or admin-only fields to participants", () => {
    render(
      <Harness
        fields={[
          field({ key: "notes", type: "short_text", label: "Internal", visibility: "admin" }),
          field({ key: "old", type: "short_text", label: "Old", hidden: true }),
          field({ key: "ok", type: "short_text", label: "Visible" }),
        ]}
      />,
    );
    expect(screen.queryByLabelText("Internal")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Old")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Visible")).toBeInTheDocument();
  });
});

describe("form rules", () => {
  it("matches the server's condition rules", () => {
    const f = field({ key: "x", type: "short_text", condition: { field: "track", equals: "Web" } });
    expect(conditionMet(f, { track: "Web" })).toBe(true);
    expect(conditionMet(f, { track: "AI; Web" })).toBe(true);
    expect(conditionMet(f, { track: "AI" })).toBe(false);
    const any = field({ key: "y", type: "short_text", condition: { field: "lunch" } });
    expect(conditionMet(any, { lunch: "FALSE" })).toBe(false);
    expect(conditionMet(any, { lunch: "TRUE" })).toBe(true);
  });

  it("requires only shown, required fields", () => {
    const fields = [
      field({ key: "a", type: "short_text", required: true }),
      field({ key: "b", type: "short_text", required: true, condition: { field: "c" } }),
      field({ key: "c", type: "checkbox" }),
      field({ key: "h", type: "section", required: true }),
    ];
    expect(missingRequired(fields, {})).toEqual({ a: "required" });
    expect(missingRequired(fields, { a: "x", c: "TRUE" })).toEqual({ b: "required" });
  });

  it("orders fields by their order value", () => {
    const fields = [field({ key: "z", type: "url", order: 9 }), field({ key: "a", type: "url", order: 1 })];
    expect(formFields(fields, {}).map((f) => f.key)).toEqual(["a", "z"]);
  });
});

describe("builder helpers", () => {
  it.each([
    ["T-shirt size", "t_shirt_size"],
    ["  Year of study ", "year_of_study"],
    ["2nd choice", "f_2nd_choice"],
    ["Café ☕", "cafe"],
    ["!!!", ""],
  ])("keyFromLabel(%j) = %j", (label, key) => {
    expect(keyFromLabel(label)).toBe(key);
  });

  it("reads the service account email from a pasted key", () => {
    expect(emailFromKey('{"type":"service_account","client_email":"ep@proj.iam.gserviceaccount.com"}')).toBe(
      "ep@proj.iam.gserviceaccount.com",
    );
    expect(emailFromKey("not json")).toBe("");
  });
});
