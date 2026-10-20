// SPDX-License-Identifier: Apache-2.0

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, type ParticipantStatus } from "../api/client";
import s from "./ui.module.css";

export { s as ui };

/**
 * Page sets the document title and moves focus to the heading when the page
 * mounts, so screen-reader users hear where a route change took them.
 */
export function Page({ title, actions, children }: { title: string; actions?: ReactNode; children: ReactNode }) {
  const ref = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    document.title = `${title} · ${document.documentElement.dataset["product"] ?? "EventPass"}`;
    ref.current?.focus();
  }, [title]);
  return (
    <>
      <div className={s.spread}>
        <h1 ref={ref} tabIndex={-1}>
          {title}
        </h1>
        {actions}
      </div>
      {children}
    </>
  );
}

export function Loading() {
  const { t } = useTranslation();
  return (
    <p role="status" className={s.row}>
      <span className={s.spinner} aria-hidden="true" /> {t("common.loading")}
    </p>
  );
}

/** Translates an error code into a message; falls back to the server text. */
export function useErrorText() {
  const { t } = useTranslation();
  return (err: unknown): string => {
    if (err instanceof ApiError) {
      return t(`errors.${err.code}`, { defaultValue: err.message });
    }
    return t("errors.internal");
  };
}

export function ErrorAlert({ error }: { error: unknown }) {
  const text = useErrorText();
  if (!error) return null;
  return (
    <p role="alert" className={`${s.alert} ${s.alertError}`}>
      {text(error)}
    </p>
  );
}

export function Alert({ kind, children }: { kind: "ok" | "warn" | "info"; children: ReactNode }) {
  const cls = { ok: s.alertOk, warn: s.alertWarn, info: s.alertInfo }[kind];
  return (
    <p role={kind === "ok" ? "status" : undefined} className={`${s.alert} ${cls}`}>
      {children}
    </p>
  );
}

const statusTone: Record<ParticipantStatus, string | undefined> = {
  pending_approval: s.badgeWarn,
  waitlisted: s.badgeWarn,
  registered: s.badgeInfo,
  confirmed: s.badgeInfo,
  checked_in: s.badgeOk,
  cancelled: s.badgeBad,
  rejected: s.badgeBad,
};

const statusIcon: Record<ParticipantStatus, string> = {
  pending_approval: "⏳",
  waitlisted: "⏳",
  registered: "•",
  confirmed: "✓",
  checked_in: "✔",
  cancelled: "✕",
  rejected: "✕",
};

/** Status shown with text and an icon, never color alone. */
export function StatusBadge({ status }: { status: ParticipantStatus }) {
  const { t } = useTranslation();
  return (
    <span className={`${s.badge} ${statusTone[status] ?? s.badgeNeutral}`}>
      <span aria-hidden="true">{statusIcon[status]}</span>
      {t(`status.${status}`)}
    </span>
  );
}

type TextFieldProps = {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  hint?: string;
  error?: string | undefined;
  required?: boolean;
  autoComplete?: string;
  placeholder?: string;
  maxLength?: number;
};

export function TextField(p: TextFieldProps) {
  const id = useId();
  const { t } = useTranslation();
  const describedBy = [p.hint ? `${id}-hint` : "", p.error ? `${id}-err` : ""].filter(Boolean).join(" ");
  return (
    <div className={s.field}>
      <label className={s.label} htmlFor={id}>
        {p.label}
      </label>
      {p.hint && (
        <span id={`${id}-hint`} className={s.hint}>
          {p.hint}
        </span>
      )}
      <input
        id={id}
        className={s.input}
        type={p.type ?? "text"}
        value={p.value}
        onChange={(e) => p.onChange(e.target.value)}
        required={p.required}
        autoComplete={p.autoComplete}
        placeholder={p.placeholder}
        maxLength={p.maxLength}
        aria-invalid={p.error ? true : undefined}
        aria-describedby={describedBy || undefined}
      />
      {p.error && (
        <span id={`${id}-err`} className={s.fieldError}>
          {t(`fieldErrors.${p.error}`, { defaultValue: p.error })}
        </span>
      )}
    </div>
  );
}

type SelectFieldProps = {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  error?: string | undefined;
};

export function SelectField(p: SelectFieldProps) {
  const id = useId();
  const { t } = useTranslation();
  return (
    <div className={s.field}>
      <label className={s.label} htmlFor={id}>
        {p.label}
      </label>
      <select
        id={id}
        className={s.input}
        value={p.value}
        onChange={(e) => p.onChange(e.target.value)}
        aria-invalid={p.error ? true : undefined}
      >
        {p.options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {p.error && <span className={s.fieldError}>{t(`fieldErrors.${p.error}`, { defaultValue: p.error })}</span>}
    </div>
  );
}

/**
 * A button that asks for confirmation in a modal dialog before a
 * destructive action. The native <dialog> traps focus and handles Escape.
 */
export function ConfirmButton({
  label,
  message,
  confirmLabel,
  onConfirm,
  small,
}: {
  label: string;
  message: string;
  confirmLabel?: string;
  onConfirm: () => void;
  small?: boolean;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal?.();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <>
      <button
        type="button"
        className={`${s.button} ${s.secondary} ${small ? s.small : ""}`}
        onClick={() => setOpen(true)}
      >
        {label}
      </button>
      <dialog ref={ref} className={s.dialog} onClose={() => setOpen(false)} aria-labelledby={undefined}>
        <p>{message}</p>
        <div className={s.row}>
          <button
            type="button"
            className={`${s.button} ${s.danger}`}
            onClick={() => {
              setOpen(false);
              onConfirm();
            }}
          >
            {confirmLabel ?? t("common.confirm")}
          </button>
          <button type="button" className={`${s.button} ${s.secondary}`} onClick={() => setOpen(false)}>
            {t("common.cancel")}
          </button>
        </div>
      </dialog>
    </>
  );
}

/** Formats a timestamp in a time zone, with the zone name shown. */
export function formatDateTime(iso: string | undefined, timeZone?: string): string {
  if (!iso) return "";
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: timeZone || undefined,
      timeZoneName: "short",
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}
