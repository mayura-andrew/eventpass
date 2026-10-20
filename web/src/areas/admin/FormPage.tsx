// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, ApiError, unwrap, type Field, type Schemas } from "../../api/client";
import { ProfileForm } from "../../components/ProfileForm";
import { ConfirmButton, ErrorAlert, Loading, Page, SelectField, TextField, ui } from "../../components/ui";
import s from "./admin.module.css";
import { useEvent } from "./EventPages";

type FieldInput = Schemas["FieldInput"];

const types: Field["type"][] = [
  "short_text",
  "long_text",
  "email",
  "phone",
  "number",
  "date",
  "single_select",
  "multi_select",
  "checkbox",
  "url",
  "section",
];
const visibilities: Field["visibility"][] = ["public", "participant", "staff", "admin"];
const editables: Field["editable"][] = ["no", "until", "until_checkin", "always"];
const hasOptions = (t: string) => t === "single_select" || t === "multi_select";

/** Turns a label into a field key: the column header in the sheet. */
export function keyFromLabel(label: string): string {
  const k = label
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 40);
  return /^[a-z]/.test(k) ? k : k ? `f_${k}`.slice(0, 40) : "";
}

export function FormPage() {
  const { t } = useTranslation();
  const event = useEvent();
  const qc = useQueryClient();
  const path = { params: { path: { eventID: event.id } } };
  const q = useQuery({
    queryKey: ["fields", event.id],
    queryFn: () => unwrap(api.GET("/events/{eventID}/fields", path)),
  });
  const [editing, setEditing] = useState<Field | "new" | null>(null);
  const [preview, setPreview] = useState<Record<string, string>>({});
  const [frame, setFrame] = useState<"phone" | "desktop">("phone");
  const refresh = () => qc.invalidateQueries({ queryKey: ["fields", event.id] });

  const order = useMutation({
    mutationFn: (keys: string[]) => unwrap(api.POST("/events/{eventID}/fields/order", { ...path, body: { keys } })),
    onSuccess: (data) => qc.setQueryData(["fields", event.id], data),
  });
  const hide = useMutation({
    mutationFn: (key: string) =>
      unwrap(api.DELETE("/events/{eventID}/fields/{key}", { params: { path: { eventID: event.id, key } } })),
    onSuccess: refresh,
  });
  const restore = useMutation({
    mutationFn: (f: Field) => unwrap(put(event.id, f.key, toInput(f))),
    onSuccess: refresh,
  });

  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorAlert error={q.error} />;
  const all = [...q.data.fields].sort((a, b) => a.order - b.order);
  const shown = all.filter((f) => !f.hidden);
  const hidden = all.filter((f) => f.hidden);
  const move = (i: number, d: -1 | 1) => {
    const keys = shown.map((f) => f.key);
    const j = i + d;
    [keys[i], keys[j]] = [keys[j] ?? "", keys[i] ?? ""];
    order.mutate([...keys, ...hidden.map((f) => f.key)]);
  };

  return (
    <Page
      title={t("admin.form.title")}
      actions={
        editing === null ? (
          <button type="button" className={ui.button} onClick={() => setEditing("new")}>
            {t("admin.form.add")}
          </button>
        ) : null
      }
    >
      <p className={ui.muted}>{t("admin.form.intro")}</p>
      <ErrorAlert error={order.error ?? hide.error ?? restore.error} />
      <div className={s.builder}>
        <div className={ui.stack}>
          {editing !== null && (
            <FieldEditor
              key={editing === "new" ? "new" : editing.key}
              eventId={event.id}
              field={editing === "new" ? null : editing}
              others={shown.filter((f) => editing === "new" || f.key !== editing.key)}
              taken={all.map((f) => f.key)}
              onDone={() => {
                setEditing(null);
                void refresh();
              }}
            />
          )}
          <ol className={s.fieldList} aria-label={t("admin.form.fields")}>
            {shown.map((f, i) => (
              <li key={f.key} className={`${ui.card} ${s.fieldItem}`}>
                <div>
                  <strong>{f.label}</strong>
                  <div className={`${ui.row} ${ui.muted}`}>
                    <span className={ui.mono}>{f.key}</span>
                    <span>· {t(`fieldTypes.${f.type}`)}</span>
                    {f.required && <span className={`${ui.badge} ${ui.badgeInfo}`}>{t("form.required")}</span>}
                    <span className={`${ui.badge} ${ui.badgeNeutral}`}>{t(`visibility.${f.visibility}`)}</span>
                  </div>
                </div>
                <div className={ui.row}>
                  <button
                    type="button"
                    className={`${ui.button} ${ui.secondary} ${ui.small}`}
                    onClick={() => setEditing(f)}
                    aria-label={t("admin.form.editField", { label: f.label })}
                  >
                    {t("admin.form.edit")}
                  </button>
                  <button
                    type="button"
                    className={`${ui.button} ${ui.secondary} ${ui.small}`}
                    disabled={i === 0 || order.isPending}
                    onClick={() => move(i, -1)}
                    aria-label={t("admin.form.moveUp", { label: f.label })}
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className={`${ui.button} ${ui.secondary} ${ui.small}`}
                    disabled={i === shown.length - 1 || order.isPending}
                    onClick={() => move(i, 1)}
                    aria-label={t("admin.form.moveDown", { label: f.label })}
                  >
                    ↓
                  </button>
                  {!f.builtin && (
                    <ConfirmButton
                      small
                      label={t("common.remove")}
                      message={t("admin.form.hideConfirm", { label: f.label })}
                      confirmLabel={t("admin.form.hide")}
                      onConfirm={() => hide.mutate(f.key)}
                    />
                  )}
                </div>
              </li>
            ))}
          </ol>
          {hidden.length > 0 && (
            <section aria-labelledby="hidden-h">
              <h2 id="hidden-h">{t("admin.form.hidden")}</h2>
              <p className={ui.muted}>{t("admin.form.hiddenIntro")}</p>
              <ul className={s.list}>
                {hidden.map((f) => (
                  <li key={f.key} className={ui.spread}>
                    <span>
                      {f.label} <span className={ui.mono}>({f.key})</span>
                    </span>
                    <button
                      type="button"
                      className={`${ui.button} ${ui.secondary} ${ui.small}`}
                      onClick={() => restore.mutate(f)}
                    >
                      {t("admin.form.restore")}
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>
        <section aria-labelledby="preview-h" className={s.previewCol}>
          <div className={ui.spread}>
            <h2 id="preview-h">{t("admin.form.preview")}</h2>
            <div className={ui.row} role="group" aria-label={t("admin.form.previewSize")}>
              {(["phone", "desktop"] as const).map((v) => (
                <button
                  key={v}
                  type="button"
                  aria-pressed={frame === v}
                  className={`${ui.button} ${ui.small} ${frame === v ? "" : ui.secondary}`}
                  onClick={() => setFrame(v)}
                >
                  {t(`admin.form.${v}`)}
                </button>
              ))}
            </div>
          </div>
          <div className={frame === "phone" ? s.phone : s.desktop}>
            <ProfileForm
              fields={shown}
              values={preview}
              onChange={(k, v) => setPreview((cur) => ({ ...cur, [k]: v }))}
            />
          </div>
        </section>
      </div>
    </Page>
  );
}

function put(eventId: string, key: string, body: FieldInput) {
  return api.PUT("/events/{eventID}/fields/{key}", { params: { path: { eventID: eventId, key } }, body });
}

function toInput(f: Field): FieldInput {
  const out: FieldInput = {
    type: f.type,
    label: f.label,
    helpText: f.helpText ?? "",
    placeholder: f.placeholder ?? "",
    required: f.required,
    options: f.options ?? [],
    validation: f.validation,
    editable: f.editable,
    visibility: f.visibility,
  };
  if (f.editableUntil) out.editableUntil = f.editableUntil;
  if (f.condition) out.condition = f.condition;
  return out;
}

function FieldEditor({
  eventId,
  field,
  others,
  taken,
  onDone,
}: {
  eventId: string;
  field: Field | null;
  others: Field[];
  taken: string[];
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const isNew = field === null;
  const [label, setLabel] = useState(field?.label ?? "");
  const [key, setKey] = useState(field?.key ?? "");
  const [keyEdited, setKeyEdited] = useState(false);
  const [type, setType] = useState<string>(field?.type ?? "short_text");
  const [required, setRequired] = useState(field?.required ?? false);
  const [options, setOptions] = useState((field?.options ?? []).join("\n"));
  const [helpText, setHelpText] = useState(field?.helpText ?? "");
  const [placeholder, setPlaceholder] = useState(field?.placeholder ?? "");
  const [visibility, setVisibility] = useState<string>(field?.visibility ?? "staff");
  const [editable, setEditable] = useState<string>(field?.editable ?? "until_checkin");
  const [until, setUntil] = useState(field?.editableUntil?.slice(0, 16) ?? "");
  const [condField, setCondField] = useState(field?.condition?.field ?? "");
  const [condEquals, setCondEquals] = useState(field?.condition?.equals ?? "");
  const [localErr, setLocalErr] = useState<Record<string, string>>({});

  const save = useMutation({
    mutationFn: () => {
      const body: FieldInput = {
        type,
        label,
        helpText,
        placeholder,
        required,
        options: hasOptions(type)
          ? options
              .split("\n")
              .map((o) => o.trim())
              .filter(Boolean)
          : [],
        validation: field?.validation ?? {},
        editable: editable as FieldInput["editable"],
        visibility: visibility as FieldInput["visibility"],
      };
      if (editable === "until" && until) body.editableUntil = new Date(until).toISOString();
      if (condField) body.condition = condEquals ? { field: condField, equals: condEquals } : { field: condField };
      return unwrap(put(eventId, key, body));
    },
    onSuccess: onDone,
  });
  const errs = { ...(save.error instanceof ApiError ? save.error.fieldErrors() : {}), ...localErr };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const local: Record<string, string> = {};
    if (!label.trim()) local["label"] = "required";
    if (isNew && !/^[a-z][a-z0-9_]{0,39}$/.test(key)) local["key"] = "invalid_key";
    else if (isNew && taken.includes(key)) local["key"] = "key_taken";
    setLocalErr(local);
    if (Object.keys(local).length === 0) save.mutate();
  };
  const builtin = field?.builtin ?? false;

  return (
    <form className={`${ui.card} ${ui.stack}`} onSubmit={submit} noValidate aria-labelledby="editor-h">
      <h2 id="editor-h">{isNew ? t("admin.form.add") : t("admin.form.editField", { label: field.label })}</h2>
      {Object.keys(errs).length === 0 && <ErrorAlert error={save.error} />}
      <div>
        <TextField
          label={t("admin.form.label")}
          value={label}
          required
          maxLength={200}
          error={errs["label"]}
          onChange={(v) => {
            setLabel(v);
            if (isNew && !keyEdited) setKey(keyFromLabel(v));
          }}
        />
        {isNew ? (
          <TextField
            label={t("admin.form.key")}
            hint={t("admin.form.keyHint")}
            value={key}
            required
            error={errs["key"]}
            onChange={(v) => {
              setKeyEdited(true);
              setKey(v);
            }}
          />
        ) : (
          <p className={ui.muted}>
            {t("admin.form.key")}: <span className={ui.mono}>{key}</span> · {t("admin.form.keyFixed")}
          </p>
        )}
        {!builtin && (
          <SelectField
            label={t("admin.form.type")}
            value={type}
            onChange={setType}
            error={errs["type"]}
            options={types.map((v) => ({ value: v, label: t(`fieldTypes.${v}`) }))}
          />
        )}
        {hasOptions(type) && (
          <TextAreaField
            label={t("admin.form.options")}
            hint={t("admin.form.optionsHint")}
            value={options}
            onChange={setOptions}
            error={errs["options"]}
          />
        )}
        {type !== "section" && (
          <>
            {!builtin && (
              <label className={s.checkRow}>
                <input type="checkbox" checked={required} onChange={(e) => setRequired(e.target.checked)} />
                {t("admin.form.required")}
              </label>
            )}
            <TextField
              label={t("admin.form.placeholder")}
              value={placeholder}
              onChange={setPlaceholder}
              maxLength={200}
            />
          </>
        )}
        <TextField label={t("admin.form.helpText")} value={helpText} onChange={setHelpText} maxLength={1000} />
        {type !== "section" && (
          <>
            <SelectField
              label={t("admin.form.visibility")}
              value={visibility}
              onChange={setVisibility}
              error={errs["visibility"]}
              options={visibilities.map((v) => ({ value: v, label: t(`visibility.${v}`) }))}
            />
            <SelectField
              label={t("admin.form.editable")}
              value={editable}
              onChange={setEditable}
              error={errs["editable"]}
              options={editables.map((v) => ({ value: v, label: t(`editable.${v}`) }))}
            />
            {editable === "until" && (
              <TextField
                label={t("admin.form.editableUntil")}
                type="datetime-local"
                value={until}
                onChange={setUntil}
                error={errs["editableUntil"]}
              />
            )}
          </>
        )}
        {!builtin && others.length > 0 && (
          <fieldset className={ui.fieldset}>
            <legend>{t("admin.form.condition")}</legend>
            <SelectField
              label={t("admin.form.conditionField")}
              value={condField}
              onChange={setCondField}
              error={errs["condition"]}
              options={[
                { value: "", label: t("admin.form.always") },
                ...others.filter((f) => f.type !== "section").map((f) => ({ value: f.key, label: f.label })),
              ]}
            />
            {condField && (
              <TextField
                label={t("admin.form.conditionEquals")}
                hint={t("admin.form.conditionEqualsHint")}
                value={condEquals}
                onChange={setCondEquals}
              />
            )}
          </fieldset>
        )}
      </div>
      <div className={ui.row}>
        <button type="submit" className={ui.button} disabled={save.isPending}>
          {save.isPending ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={onDone}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}

function TextAreaField(p: {
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  error?: string | undefined;
}) {
  const { t } = useTranslation();
  return (
    <div className={ui.field}>
      <label className={ui.label}>
        {p.label}
        {p.hint && <span className={`${ui.hint} ${s.block}`}>{p.hint}</span>}
        <textarea
          className={`${ui.input} ${s.textarea}`}
          value={p.value}
          onChange={(e) => p.onChange(e.target.value)}
          aria-invalid={p.error ? true : undefined}
        />
      </label>
      {p.error && <span className={ui.fieldError}>{t(`fieldErrors.${p.error}`, { defaultValue: p.error })}</span>}
    </div>
  );
}
