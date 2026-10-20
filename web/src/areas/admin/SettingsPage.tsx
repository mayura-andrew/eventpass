// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, ApiError, unwrap } from "../../api/client";
import { Alert, ErrorAlert, Loading, Page, TextField, ui } from "../../components/ui";
import { contrastRatio } from "../../theme";
import { useCan } from "./AdminArea";

const KEYS = [
  ["product_name", "productName"],
  ["primary_color", "primaryColor"],
  ["accent_color", "accentColor"],
  ["time_zone", "timeZone"],
  ["support_email", "supportEmail"],
  ["footer_text", "footerText"],
  ["privacy_url", "privacyUrl"],
  ["terms_url", "termsUrl"],
] as const;

export function SettingsPage() {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["workspace-settings"], queryFn: () => unwrap(api.GET("/workspace/settings")) });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorAlert error={q.error} />;
  return (
    <Page title={t("admin.settings.title")}>
      <SettingsForm initial={q.data.settings} />
    </Page>
  );
}

function SettingsForm({ initial }: { initial: Record<string, string> }) {
  const { t } = useTranslation();
  const can = useCan();
  const qc = useQueryClient();
  const [values, setValues] = useState<Record<string, string>>(initial);
  const save = useMutation({
    mutationFn: () => {
      const settings: Record<string, string> = {};
      for (const [k] of KEYS) settings[k] = values[k] ?? "";
      return unwrap(api.PATCH("/workspace/settings", { body: { settings } }));
    },
    onSuccess: (data) => {
      qc.setQueryData(["workspace-settings"], data);
      return qc.invalidateQueries({ queryKey: ["setup"] });
    },
  });
  const errs = save.error instanceof ApiError ? save.error.fieldErrors() : {};
  const primary = values["primary_color"] ?? "";
  // Checked live as the owner types; the server repeats the check on save.
  const lowContrast = /^#[0-9a-f]{6}$/i.test(primary) && contrastRatio(primary, "#ffffff") < 4.5;
  const readOnly = !can("workspace.write");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <>
      {save.isSuccess && <Alert kind="ok">{t("common.saved")}</Alert>}
      {Object.keys(errs).length === 0 && <ErrorAlert error={save.error} />}
      {lowContrast && <Alert kind="warn">{t("admin.settings.lowContrast")}</Alert>}
      <form className={ui.card} onSubmit={submit} noValidate>
        <fieldset disabled={readOnly} className={ui.fieldset}>
          {KEYS.map(([key, label]) => (
            <TextField
              key={key}
              label={t(`admin.settings.${label}`)}
              hint={key === "product_name" ? t("admin.settings.productNameHint") : undefined}
              type={key.endsWith("_url") ? "url" : key === "support_email" ? "email" : "text"}
              placeholder={key.endsWith("_color") ? "#1B4DDB" : undefined}
              value={values[key] ?? ""}
              error={errs[key]}
              onChange={(v) => setValues({ ...values, [key]: v })}
            />
          ))}
          {!readOnly && (
            <button type="submit" className={ui.button} disabled={save.isPending}>
              {save.isPending ? t("common.saving") : t("common.save")}
            </button>
          )}
        </fieldset>
      </form>
    </>
  );
}
