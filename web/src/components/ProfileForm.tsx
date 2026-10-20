// SPDX-License-Identifier: Apache-2.0

import { useId } from "react";
import { useTranslation } from "react-i18next";
import type { Field } from "../api/client";
import s from "./ProfileForm.module.css";
import { ui } from "./ui";

/** Joins multi-select values in one cell, as the server stores them. */
export const MULTI_SEP = "; ";

/** Mirrors domain.ConditionMet: is the field shown for these values? */
export function conditionMet(f: Field, values: Record<string, string>): boolean {
  const c = f.condition;
  if (!c?.field) return true;
  const v = (values[c.field] ?? "").trim();
  if (!c.equals) return v !== "" && v.toLowerCase() !== "false";
  return v.split(MULTI_SEP).includes(c.equals);
}

/** Fields a participant fills in, in order, with conditions applied. */
export function formFields(fields: Field[], values: Record<string, string>): Field[] {
  return [...fields]
    .filter((f) => !f.hidden && f.visibility !== "admin")
    .sort((a, b) => a.order - b.order)
    .filter((f) => conditionMet(f, values));
}

/** Client-side check for required fields; the server validates everything. */
export function missingRequired(fields: Field[], values: Record<string, string>): Record<string, string> {
  const errs: Record<string, string> = {};
  for (const f of formFields(fields, values)) {
    if (f.required && f.type !== "section" && !(values[f.key] ?? "").trim()) errs[f.key] = "required";
  }
  return errs;
}

type Props = {
  fields: Field[];
  values: Record<string, string>;
  onChange: (key: string, value: string) => void;
  errors?: Record<string, string>;
  /** Keys shown but not editable, with the reason. */
  locked?: Record<string, string>;
  disabled?: boolean;
};

/** Renders a participant profile form from the event's field schema. */
export function ProfileForm({ fields, values, onChange, errors = {}, locked = {}, disabled }: Props) {
  return (
    <>
      {formFields(fields, values).map((f) => (
        <FieldControl
          key={f.key}
          field={f}
          value={values[f.key] ?? ""}
          onChange={(v) => onChange(f.key, v)}
          error={errors[f.key]}
          lockedReason={locked[f.key]}
          disabled={disabled}
        />
      ))}
    </>
  );
}

type ControlProps = {
  field: Field;
  value: string;
  onChange: (v: string) => void;
  error?: string | undefined;
  lockedReason?: string | undefined;
  disabled?: boolean | undefined;
};

const inputType: Partial<Record<Field["type"], string>> = {
  email: "email",
  phone: "tel",
  number: "number",
  date: "date",
  url: "url",
};

const autoComplete: Record<string, string> = { full_name: "name", email: "email" };

export function FieldControl({ field: f, value, onChange, error, lockedReason, disabled }: ControlProps) {
  const id = useId();
  const { t } = useTranslation();
  const off = disabled || !!lockedReason;
  if (f.type === "section") {
    return (
      <div className={s.section}>
        <h3>{f.label}</h3>
        {f.helpText && <p className={ui.muted}>{f.helpText}</p>}
      </div>
    );
  }
  const describedBy = [f.helpText ? `${id}-hint` : "", lockedReason ? `${id}-lock` : "", error ? `${id}-err` : ""]
    .filter(Boolean)
    .join(" ");
  const aria = {
    "aria-invalid": error ? true : undefined,
    "aria-describedby": describedBy || undefined,
    "aria-required": f.required || undefined,
  } as const;
  const label = (
    <>
      {f.label}
      {f.required && <span className={s.req}> ({t("form.required")})</span>}
    </>
  );
  const hint = f.helpText && (
    <span id={`${id}-hint`} className={ui.hint}>
      {f.helpText}
    </span>
  );
  const after = (
    <>
      {lockedReason && (
        <span id={`${id}-lock`} className={ui.hint}>
          <span aria-hidden="true">🔒 </span>
          {lockedReason}
        </span>
      )}
      {error && (
        <span id={`${id}-err`} className={ui.fieldError}>
          {t(`fieldErrors.${error}`, { defaultValue: error })}
        </span>
      )}
    </>
  );

  if (f.type === "checkbox" || f.type === "consent") {
    return (
      <div className={ui.field}>
        <div className={s.check}>
          <input
            id={id}
            type="checkbox"
            checked={value.toUpperCase() === "TRUE"}
            onChange={(e) => onChange(e.target.checked ? "TRUE" : "")}
            disabled={off}
            {...aria}
          />
          <label htmlFor={id}>{label}</label>
        </div>
        {hint}
        {after}
      </div>
    );
  }

  const options = f.options ?? [];
  if (f.type === "multi_select" || (f.type === "single_select" && options.length <= 4)) {
    const multi = f.type === "multi_select";
    const picked = value ? value.split(MULTI_SEP) : [];
    const toggle = (o: string, on: boolean) => {
      // Keep the admin's option order so the cell reads the same for everyone.
      const next = options.filter((x) => (x === o ? on : picked.includes(x)));
      onChange(next.join(MULTI_SEP));
    };
    return (
      <fieldset
        className={`${ui.field} ${s.choices}`}
        aria-invalid={aria["aria-invalid"]}
        aria-describedby={aria["aria-describedby"]}
      >
        <legend className={ui.label}>{label}</legend>
        {hint}
        {options.map((o) => (
          <label key={o} className={s.check}>
            <input
              type={multi ? "checkbox" : "radio"}
              name={id}
              value={o}
              checked={picked.includes(o)}
              onChange={(e) => (multi ? toggle(o, e.target.checked) : onChange(o))}
              disabled={off}
            />
            {o}
          </label>
        ))}
        {after}
      </fieldset>
    );
  }

  let control;
  if (f.type === "single_select") {
    control = (
      <select
        id={id}
        className={ui.input}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={off}
        {...aria}
      >
        <option value="">{t("form.choose")}</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    );
  } else if (f.type === "long_text") {
    control = (
      <textarea
        id={id}
        className={`${ui.input} ${s.textarea}`}
        value={value}
        placeholder={f.placeholder}
        maxLength={f.validation.maxLength || undefined}
        onChange={(e) => onChange(e.target.value)}
        disabled={off}
        {...aria}
      />
    );
  } else if (f.type === "photo" || f.type === "file") {
    control = (
      <p id={id} className={ui.muted}>
        {t("form.uploadsLater")}
      </p>
    );
  } else {
    control = (
      <input
        id={id}
        className={ui.input}
        type={inputType[f.type] ?? "text"}
        value={value}
        placeholder={f.placeholder}
        autoComplete={autoComplete[f.key]}
        inputMode={f.type === "number" ? "decimal" : undefined}
        maxLength={f.validation.maxLength || undefined}
        onChange={(e) => onChange(e.target.value)}
        disabled={off}
        {...aria}
      />
    );
  }
  return (
    <div className={ui.field}>
      <label className={ui.label} htmlFor={id}>
        {label}
      </label>
      {hint}
      {control}
      {after}
    </div>
  );
}
