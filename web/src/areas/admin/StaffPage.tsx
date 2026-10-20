// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, ApiError, unwrap, type EventRole } from "../../api/client";
import { Alert, ConfirmButton, ErrorAlert, Loading, Page, SelectField, TextField, ui } from "../../components/ui";
import { useEvent } from "./EventPages";

export function StaffPage() {
  const { t } = useTranslation();
  const event = useEvent();
  const qc = useQueryClient();
  const path = { params: { path: { eventID: event.id } } };
  const roles = useQuery({
    queryKey: ["roles", event.id],
    queryFn: () => unwrap(api.GET("/events/{eventID}/roles", path)),
  });
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<EventRole>("staff");
  const grant = useMutation({
    mutationFn: () => unwrap(api.POST("/events/{eventID}/roles", { ...path, body: { email, name, role } })),
    onSuccess: async () => {
      setEmail("");
      setName("");
      await qc.invalidateQueries({ queryKey: ["roles", event.id] });
    },
  });
  const revoke = useMutation({
    mutationFn: (userID: string) =>
      unwrap(api.DELETE("/events/{eventID}/roles/{userID}", { params: { path: { eventID: event.id, userID } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["roles", event.id] }),
  });
  const errs = grant.error instanceof ApiError ? grant.error.fieldErrors() : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    grant.mutate();
  };
  const roleOptions = (["staff", "viewer", "admin"] as const).map((r) => ({ value: r, label: t(`roles.${r}`) }));

  return (
    <Page title={t("admin.staff.title")}>
      <p>{t("admin.staff.intro")}</p>
      {grant.isSuccess && <Alert kind="ok">{t("admin.staff.added")}</Alert>}
      <form className={ui.card} onSubmit={submit} noValidate>
        <h2>{t("admin.staff.add")}</h2>
        {Object.keys(errs).length === 0 && <ErrorAlert error={grant.error} />}
        <TextField
          label={t("admin.staff.email")}
          type="email"
          value={email}
          onChange={setEmail}
          required
          error={errs["email"]}
          autoComplete="off"
        />
        <TextField
          label={t("admin.staff.name")}
          value={name}
          onChange={setName}
          error={errs["name"]}
          autoComplete="off"
        />
        <SelectField
          label={t("admin.staff.role")}
          value={role}
          onChange={(v) => setRole(v as EventRole)}
          options={roleOptions}
        />
        <button type="submit" className={ui.button} disabled={grant.isPending || !email.trim()}>
          {t("admin.staff.add")}
        </button>
      </form>
      <h2>{t("admin.event.staff")}</h2>
      <ErrorAlert error={roles.error ?? revoke.error} />
      {roles.isPending && <Loading />}
      {roles.data && roles.data.roles.length === 0 && (
        <p className={`${ui.card} ${ui.empty}`}>{t("admin.staff.empty")}</p>
      )}
      {roles.data && roles.data.roles.length > 0 && (
        <div className={ui.tableWrap}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">{t("admin.staff.name")}</th>
                <th scope="col">{t("admin.staff.email")}</th>
                <th scope="col">{t("admin.staff.role")}</th>
                <th scope="col">
                  <span className="visually-hidden">{t("common.remove")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {roles.data.roles.map((r) => (
                <tr key={r.user.id}>
                  <td>{r.user.name || "—"}</td>
                  <td>{r.user.email}</td>
                  <td>{t(`roles.${r.role}`)}</td>
                  <td>
                    <ConfirmButton
                      small
                      label={t("common.remove")}
                      message={t("admin.staff.removeConfirm", { name: r.user.name || r.user.email })}
                      confirmLabel={t("common.remove")}
                      onConfirm={() => revoke.mutate(r.user.id)}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  );
}
