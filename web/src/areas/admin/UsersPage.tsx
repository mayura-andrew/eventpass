// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, ApiError, unwrap, type User } from "../../api/client";
import {
  ConfirmButton,
  ErrorAlert,
  formatDateTime,
  Loading,
  Page,
  SelectField,
  TextField,
  ui,
} from "../../components/ui";
import { useCan } from "./AdminArea";

type Role = User["workspaceRole"];

export function UsersPage() {
  const { t } = useTranslation();
  const can = useCan();
  const manage = can("users.manage");
  const qc = useQueryClient();
  const users = useQuery({ queryKey: ["users"], queryFn: () => unwrap(api.GET("/workspace/users")) });
  const update = useMutation({
    mutationFn: (v: {
      id: string;
      body: { workspaceRole?: Role; status?: "active" | "disabled"; signOutEverywhere?: boolean };
    }) => unwrap(api.PATCH("/workspace/users/{userID}", { params: { path: { userID: v.id } }, body: v.body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });
  const roleOptions = (["owner", "admin", "none"] as const).map((r) => ({ value: r, label: t(`roles.${r}`) }));

  return (
    <Page title={t("admin.users.title")}>
      <p>{t("admin.users.intro")}</p>
      {manage && <AddUser />}
      <ErrorAlert error={users.error ?? update.error} />
      {users.isPending && <Loading />}
      {users.data && (
        <div className={ui.tableWrap}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">{t("admin.staff.name")}</th>
                <th scope="col">{t("admin.staff.email")}</th>
                <th scope="col">{t("admin.staff.role")}</th>
                <th scope="col">{t("admin.users.lastLogin")}</th>
                {manage && (
                  <th scope="col">
                    <span className="visually-hidden">{t("common.save")}</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {users.data.users.map((u) => (
                <tr key={u.id}>
                  <td>
                    {u.name || "—"}
                    {u.status === "disabled" && (
                      <>
                        {" "}
                        <span className={`${ui.badge} ${ui.badgeBad}`}>{t("admin.users.disabled")}</span>
                      </>
                    )}
                  </td>
                  <td>{u.email}</td>
                  <td>
                    {manage ? (
                      <SelectField
                        label={`${t("admin.staff.role")}: ${u.email}`}
                        value={u.workspaceRole}
                        options={roleOptions}
                        onChange={(v) => update.mutate({ id: u.id, body: { workspaceRole: v as Role } })}
                      />
                    ) : (
                      t(`roles.${u.workspaceRole}`)
                    )}
                  </td>
                  <td>{u.lastLoginAt ? formatDateTime(u.lastLoginAt) : t("admin.users.never")}</td>
                  {manage && (
                    <td>
                      <div className={ui.row}>
                        {u.status === "active" ? (
                          <ConfirmButton
                            small
                            label={t("admin.users.disable")}
                            message={t("admin.users.disableConfirm", { name: u.name || u.email })}
                            confirmLabel={t("admin.users.disable")}
                            onConfirm={() => update.mutate({ id: u.id, body: { status: "disabled" } })}
                          />
                        ) : (
                          <button
                            type="button"
                            className={`${ui.button} ${ui.secondary} ${ui.small}`}
                            onClick={() => update.mutate({ id: u.id, body: { status: "active" } })}
                          >
                            {t("admin.users.enable")}
                          </button>
                        )}
                        <button
                          type="button"
                          className={`${ui.button} ${ui.secondary} ${ui.small}`}
                          onClick={() => update.mutate({ id: u.id, body: { signOutEverywhere: true } })}
                        >
                          {t("admin.users.signOutAll")}
                        </button>
                      </div>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  );
}

function AddUser() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("admin");
  const create = useMutation({
    mutationFn: () => unwrap(api.POST("/workspace/users", { body: { email, name, workspaceRole: role } })),
    onSuccess: async () => {
      setEmail("");
      setName("");
      await qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
  const errs = create.error instanceof ApiError ? create.error.fieldErrors() : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  return (
    <form className={ui.card} onSubmit={submit} noValidate>
      <h2>{t("admin.users.add")}</h2>
      {Object.keys(errs).length === 0 && <ErrorAlert error={create.error} />}
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
        onChange={(v) => setRole(v as Role)}
        options={(["admin", "owner", "none"] as const).map((r) => ({ value: r, label: t(`roles.${r}`) }))}
      />
      <button type="submit" className={ui.button} disabled={create.isPending || !email.trim()}>
        {t("admin.users.add")}
      </button>
    </form>
  );
}
