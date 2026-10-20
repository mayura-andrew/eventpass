// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, ApiError, unwrap, type EventSummary, type Schemas } from "../../api/client";
import { Alert, ErrorAlert, Page, SelectField, TextField, ui } from "../../components/ui";
import s from "./admin.module.css";
import { useEvent } from "./EventPages";

const statuses: Schemas["EventStatus"][] = ["draft", "published", "registration_closed", "finished", "archived"];

/** "2026-11-21T09:00" for a datetime-local input, in the browser's zone. */
export function toLocalInput(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(v: string): string | undefined {
  if (!v) return undefined;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

export function EventSettingsPage() {
  const { t } = useTranslation();
  const event = useEvent();
  return (
    <Page title={t("admin.eventSettings.title")}>
      {!event.sheetConnected && <ConnectSheet event={event} />}
      <SettingsForm key={event.id} event={event} />
    </Page>
  );
}

function ConnectSheet({ event }: { event: EventSummary }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [link, setLink] = useState("");
  const [copied, setCopied] = useState(false);
  const connect = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/events/{eventID}/connect-sheet", {
          params: { path: { eventID: event.id } },
          body: { spreadsheet: link },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["event", event.id] }),
  });
  const email = event.serviceAccountEmail;
  return (
    <form
      className={`${ui.card} ${ui.stack}`}
      aria-labelledby="connect-h"
      onSubmit={(e) => {
        e.preventDefault();
        connect.mutate();
      }}
    >
      <h2 id="connect-h">{t("admin.connect.title")}</h2>
      <ol className={ui.stack}>
        <li>{t("admin.connect.step1")}</li>
        <li>
          {t("admin.connect.step2")}
          {email && (
            <div className={ui.row}>
              <span className={s.code}>{email}</span>
              <button
                type="button"
                className={`${ui.button} ${ui.secondary} ${ui.small}`}
                onClick={() => {
                  void navigator.clipboard?.writeText(email).then(() => setCopied(true));
                }}
              >
                {copied ? t("common.copied") : t("common.copy")}
              </button>
            </div>
          )}
        </li>
        <li>{t("admin.connect.step3")}</li>
      </ol>
      <ErrorAlert error={connect.error} />
      <TextField
        label={t("admin.connect.link")}
        placeholder="https://docs.google.com/spreadsheets/d/…"
        value={link}
        onChange={setLink}
        required
      />
      <div>
        <button type="submit" className={ui.button} disabled={connect.isPending || !link.trim()}>
          {connect.isPending ? t("admin.connect.connecting") : t("admin.connect.submit")}
        </button>
      </div>
    </form>
  );
}

function SettingsForm({ event }: { event: EventSummary }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const st = event.settings ?? {};
  const [name, setName] = useState(event.name);
  const [slug, setSlug] = useState(event.slug);
  const [status, setStatus] = useState<string>(event.status);
  const [timeZone, setTimeZone] = useState(event.timeZone);
  const [startsAt, setStartsAt] = useState(toLocalInput(event.startsAt));
  const [endsAt, setEndsAt] = useState(toLocalInput(event.endsAt));
  const [settings, setSettings] = useState<Record<string, string>>({
    description: st["description"] ?? "",
    venue_name: st["venue_name"] ?? "",
    venue_address: st["venue_address"] ?? "",
    capacity: st["capacity"] ?? "",
    registration_mode: st["registration_mode"] || "open",
    registration_opens: toLocalInput(st["registration_opens"]),
    registration_closes: toLocalInput(st["registration_closes"]),
    consent_text: st["consent_text"] ?? "",
    support_email: st["support_email"] ?? "",
  });
  const set = (k: string) => (v: string) => setSettings((cur) => ({ ...cur, [k]: v }));
  const save = useMutation({
    mutationFn: () => {
      const body: Schemas["EventInput"] = { name, slug, status: status as Schemas["EventStatus"], timeZone };
      const sa = fromLocalInput(startsAt);
      const ea = fromLocalInput(endsAt);
      if (sa) body.startsAt = sa;
      if (ea) body.endsAt = ea;
      if (event.sheetConnected) {
        const out = { ...settings };
        out["registration_opens"] = fromLocalInput(settings["registration_opens"] ?? "") ?? "";
        out["registration_closes"] = fromLocalInput(settings["registration_closes"] ?? "") ?? "";
        body.settings = out;
      }
      return unwrap(api.PATCH("/events/{eventID}", { params: { path: { eventID: event.id } }, body }));
    },
    onSuccess: (e) => {
      qc.setQueryData(["event", event.id], e);
      void qc.invalidateQueries({ queryKey: ["events"] });
    },
  });
  const errs = save.error instanceof ApiError ? save.error.fieldErrors() : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const sheet = event.sheetConnected;
  return (
    <form className={ui.stack} onSubmit={submit} noValidate>
      {save.isSuccess && <Alert kind="ok">{t("common.saved")}</Alert>}
      {Object.keys(errs).length === 0 && <ErrorAlert error={save.error} />}
      <section className={ui.card} aria-labelledby="basics-h">
        <h2 id="basics-h">{t("admin.eventSettings.basics")}</h2>
        <TextField
          label={t("admin.events.name")}
          value={name}
          onChange={setName}
          required
          maxLength={120}
          error={errs["name"]}
        />
        <TextField
          label={t("admin.events.slug")}
          hint={t("admin.events.slugHint", { slug: slug || "…" })}
          value={slug}
          onChange={setSlug}
          required
          error={errs["slug"]}
        />
        <SelectField
          label={t("admin.eventSettings.status")}
          value={status}
          onChange={setStatus}
          error={errs["status"]}
          options={statuses.map((v) => ({ value: v, label: t(`eventStatus.${v}`) }))}
        />
        {status === "published" && !sheet && <Alert kind="warn">{t("admin.eventSettings.publishNeedsSheet")}</Alert>}
        <TextField
          label={t("admin.eventSettings.startsAt")}
          type="datetime-local"
          value={startsAt}
          onChange={setStartsAt}
          error={errs["startsAt"]}
        />
        <TextField
          label={t("admin.eventSettings.endsAt")}
          type="datetime-local"
          value={endsAt}
          onChange={setEndsAt}
          error={errs["endsAt"]}
        />
        <TextField
          label={t("admin.eventSettings.timeZone")}
          hint={t("admin.eventSettings.timeZoneHint")}
          value={timeZone}
          onChange={setTimeZone}
          error={errs["timeZone"]}
        />
      </section>
      {sheet && (
        <>
          <section className={ui.card} aria-labelledby="venue-h">
            <h2 id="venue-h">{t("admin.eventSettings.venue")}</h2>
            <TextField
              label={t("admin.eventSettings.venueName")}
              value={settings["venue_name"] ?? ""}
              onChange={set("venue_name")}
              maxLength={200}
              error={errs["settings.venue_name"]}
            />
            <TextField
              label={t("admin.eventSettings.venueAddress")}
              value={settings["venue_address"] ?? ""}
              onChange={set("venue_address")}
              maxLength={500}
              error={errs["settings.venue_address"]}
            />
            <LongText
              label={t("admin.eventSettings.description")}
              value={settings["description"] ?? ""}
              onChange={set("description")}
            />
            <TextField
              label={t("admin.eventSettings.supportEmail")}
              type="email"
              value={settings["support_email"] ?? ""}
              onChange={set("support_email")}
              error={errs["settings.support_email"]}
            />
          </section>
          <section className={ui.card} aria-labelledby="reg-h">
            <h2 id="reg-h">{t("admin.eventSettings.registration")}</h2>
            <SelectField
              label={t("admin.eventSettings.mode")}
              value={settings["registration_mode"] ?? "open"}
              onChange={set("registration_mode")}
              error={errs["settings.registration_mode"]}
              options={[
                { value: "open", label: t("admin.eventSettings.modeOpen") },
                { value: "approval", label: t("admin.eventSettings.modeApproval") },
              ]}
            />
            <TextField
              label={t("admin.eventSettings.capacity")}
              hint={t("admin.eventSettings.capacityHint")}
              type="number"
              value={settings["capacity"] ?? ""}
              onChange={set("capacity")}
              error={errs["settings.capacity"]}
            />
            <TextField
              label={t("admin.eventSettings.opens")}
              type="datetime-local"
              value={settings["registration_opens"] ?? ""}
              onChange={set("registration_opens")}
              error={errs["settings.registration_opens"]}
            />
            <TextField
              label={t("admin.eventSettings.closes")}
              type="datetime-local"
              value={settings["registration_closes"] ?? ""}
              onChange={set("registration_closes")}
              error={errs["settings.registration_closes"]}
            />
            <LongText
              label={t("admin.eventSettings.consent")}
              hint={t("admin.eventSettings.consentHint", { version: st["consent_version"] || "–" })}
              value={settings["consent_text"] ?? ""}
              onChange={set("consent_text")}
            />
          </section>
        </>
      )}
      <div>
        <button type="submit" className={ui.button} disabled={save.isPending}>
          {save.isPending ? t("common.saving") : t("common.save")}
        </button>
      </div>
    </form>
  );
}

function LongText(p: { label: string; hint?: string; value: string; onChange: (v: string) => void }) {
  return (
    <div className={ui.field}>
      <label className={ui.label}>
        {p.label}
        {p.hint && <span className={`${ui.hint} ${s.block}`}>{p.hint}</span>}
        <textarea
          className={`${ui.input} ${s.textarea}`}
          value={p.value}
          onChange={(e) => p.onChange(e.target.value)}
        />
      </label>
    </div>
  );
}
