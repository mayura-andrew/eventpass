// SPDX-License-Identifier: Apache-2.0

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, ApiError, unwrap, type Field, type Participant, type ParticipantStatus } from "../../api/client";
import {
  Alert,
  ErrorAlert,
  formatDateTime,
  Loading,
  Page,
  SelectField,
  StatusBadge,
  TextField,
  ui,
} from "../../components/ui";
import s from "./admin.module.css";
import { useEvent } from "./EventPages";

const STATUSES: ParticipantStatus[] = [
  "pending_approval",
  "waitlisted",
  "registered",
  "confirmed",
  "checked_in",
  "cancelled",
  "rejected",
];
const PAGE = 50;

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

export function ParticipantsPage() {
  const { t } = useTranslation();
  const event = useEvent();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
  const [editing, setEditing] = useState<Participant | null>(null);
  const [saved, setSaved] = useState(false);
  const query = useDebounced(q, 250);

  const fields = useQuery({
    queryKey: ["fields", event.id],
    queryFn: () => unwrap(api.GET("/events/{eventID}/fields", { params: { path: { eventID: event.id } } })),
  });
  const list = useQuery({
    queryKey: ["participants", event.id, query, status, offset],
    queryFn: () =>
      unwrap(
        api.GET("/events/{eventID}/participants", {
          params: {
            path: { eventID: event.id },
            query: { q: query || undefined, status: status || undefined, limit: PAGE, offset },
          },
        }),
      ),
    placeholderData: keepPreviousData,
  });
  const columns = (fields.data?.fields ?? [])
    .filter((f) => !f.builtin && !f.hidden && f.type !== "section")
    .slice(0, 3);
  const total = list.data?.total ?? 0;
  const isAdmin = event.role === "admin";

  return (
    <Page title={t("admin.participants.title")}>
      {saved && <Alert kind="ok">{t("admin.participants.saved")}</Alert>}
      {editing && fields.data && (
        <EditParticipant
          participant={editing}
          fields={fields.data.fields}
          onDone={(ok) => {
            setEditing(null);
            setSaved(ok);
          }}
        />
      )}
      <div className={s.filters} role="search">
        <TextField
          label={t("admin.participants.search")}
          value={q}
          onChange={(v) => {
            setQ(v);
            setOffset(0);
          }}
          type="search"
        />
        <SelectField
          label={t("admin.participants.statusFilter")}
          value={status}
          onChange={(v) => {
            setStatus(v);
            setOffset(0);
          }}
          options={[
            { value: "", label: t("admin.participants.allStatuses") },
            ...STATUSES.map((v) => ({ value: v, label: t(`status.${v}`) })),
          ]}
        />
      </div>
      <ErrorAlert error={list.error} />
      {list.isPending && <Loading />}
      {list.data && list.data.participants.length === 0 && (
        <p className={`${ui.card} ${ui.empty}`}>
          {query || status ? t("admin.participants.empty") : t("admin.participants.none")}
        </p>
      )}
      {list.data && list.data.participants.length > 0 && (
        <>
          <p className={ui.muted} aria-live="polite">
            {t("common.showing", { from: offset + 1, to: offset + list.data.participants.length, total })}
          </p>
          <div className={ui.tableWrap}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">{t("admin.participants.name")}</th>
                  <th scope="col">{t("admin.participants.passId")}</th>
                  <th scope="col">{t("admin.participants.status")}</th>
                  {columns.map((f) => (
                    <th scope="col" key={f.key}>
                      {f.label}
                    </th>
                  ))}
                  <th scope="col">{t("admin.participants.registered")}</th>
                </tr>
              </thead>
              <tbody>
                {list.data.participants.map((p) => (
                  <tr key={p.id}>
                    <td>
                      {isAdmin ? (
                        <button
                          type="button"
                          className={s.rowLink}
                          onClick={() => {
                            setSaved(false);
                            setEditing(p);
                          }}
                        >
                          {p.fullName}
                        </button>
                      ) : (
                        p.fullName
                      )}
                      <br />
                      <span className={ui.muted}>{p.email}</span>
                    </td>
                    <td className={ui.mono}>{p.passId}</td>
                    <td>
                      <StatusBadge status={p.status} />
                    </td>
                    {columns.map((f) => (
                      <td key={f.key}>{p.values[f.key] ?? ""}</td>
                    ))}
                    <td>{formatDateTime(p.registeredAt, event.timeZone)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className={ui.row}>
            <button
              type="button"
              className={`${ui.button} ${ui.secondary}`}
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - PAGE))}
            >
              {t("common.previous")}
            </button>
            <button
              type="button"
              className={`${ui.button} ${ui.secondary}`}
              disabled={offset + PAGE >= total}
              onClick={() => setOffset(offset + PAGE)}
            >
              {t("common.next")}
            </button>
          </div>
        </>
      )}
    </Page>
  );
}

function EditParticipant({
  participant,
  fields,
  onDone,
}: {
  participant: Participant;
  fields: Field[];
  onDone: (saved: boolean) => void;
}) {
  const { t } = useTranslation();
  const event = useEvent();
  const qc = useQueryClient();
  const notesId = useId();
  const [status, setStatus] = useState<ParticipantStatus>(participant.status);
  const [notes, setNotes] = useState(participant.notes ?? "");
  const [values, setValues] = useState<Record<string, string>>({
    full_name: participant.fullName,
    ...participant.values,
  });
  const editable = fields.filter((f) => !f.hidden && f.type !== "section" && f.key !== "email");
  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PATCH("/events/{eventID}/participants/{participantID}", {
          params: { path: { eventID: event.id, participantID: participant.id } },
          body: { status: status === participant.status ? undefined : status, notes, values },
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["participants", event.id] });
      await qc.invalidateQueries({ queryKey: ["stats", event.id] });
      onDone(true);
    },
  });
  const errs = save.error instanceof ApiError ? save.error.fieldErrors() : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <form className={`${ui.card} ${ui.stack}`} onSubmit={submit} noValidate aria-labelledby="edit-h">
      <h2 id="edit-h">
        {t("admin.participants.edit")}: {participant.fullName} <span className={ui.mono}>{participant.passId}</span>
      </h2>
      {Object.keys(errs).length === 0 && <ErrorAlert error={save.error} />}
      <div>
        <SelectField
          label={t("admin.participants.changeStatus")}
          value={status}
          onChange={(v) => setStatus(v as ParticipantStatus)}
          options={STATUSES.map((v) => ({ value: v, label: t(`status.${v}`) }))}
        />
        {editable.map((f) =>
          f.type === "single_select" ? (
            <SelectField
              key={f.key}
              label={f.label}
              value={values[f.key] ?? ""}
              error={errs[f.key]}
              onChange={(v) => setValues({ ...values, [f.key]: v })}
              options={[{ value: "", label: "—" }, ...(f.options ?? []).map((o) => ({ value: o, label: o }))]}
            />
          ) : f.type === "checkbox" ? (
            <SelectField
              key={f.key}
              label={f.label}
              value={values[f.key] ?? "FALSE"}
              error={errs[f.key]}
              onChange={(v) => setValues({ ...values, [f.key]: v })}
              options={[
                { value: "TRUE", label: "✓" },
                { value: "FALSE", label: "—" },
              ]}
            />
          ) : (
            <TextField
              key={f.key}
              label={f.label}
              value={values[f.key] ?? ""}
              error={errs[f.key]}
              onChange={(v) => setValues({ ...values, [f.key]: v })}
            />
          ),
        )}
        <div className={ui.field}>
          <label className={ui.label} htmlFor={notesId}>
            {t("admin.participants.notes")}
          </label>
          <textarea
            id={notesId}
            className={ui.input}
            rows={3}
            value={notes}
            maxLength={5000}
            onChange={(e) => setNotes(e.target.value)}
          />
        </div>
      </div>
      <div className={ui.row}>
        <button type="submit" className={ui.button} disabled={save.isPending}>
          {save.isPending ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={() => onDone(false)}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
