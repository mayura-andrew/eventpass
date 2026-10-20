// SPDX-License-Identifier: Apache-2.0

import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, ApiError, unwrap } from "../api/client";
import { useSetup } from "../session";
import { Alert, ErrorAlert, Page, TextField, ui } from "./ui";

export function LoginPage({ audience }: { audience: "staff" | "participant" }) {
  const { t } = useTranslation();
  const { data: setup } = useSetup();
  const [email, setEmail] = useState("");
  const send = useMutation({
    mutationFn: (addr: string) => unwrap(api.POST("/auth/magic-link", { body: { email: addr, audience } })),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    send.mutate(email.trim());
  };
  const fieldError = send.error instanceof ApiError ? send.error.fieldErrors()["email"] : undefined;
  const title = audience === "staff" ? t("login.staffTitle") : t("login.participantTitle");

  return (
    <main id="main" className={ui.centered}>
      <div className={`${ui.narrow} ${ui.stack}`}>
        <p className={ui.muted}>{setup?.productName}</p>
        <div className={ui.card}>
          {send.isSuccess ? (
            <Page title={t("login.sentTitle")}>
              <p role="status">{t("login.sentBody", { email })}</p>
              <button
                type="button"
                className={`${ui.button} ${ui.secondary}`}
                onClick={() => {
                  send.reset();
                  setEmail("");
                }}
              >
                {t("login.tryAgain")}
              </button>
            </Page>
          ) : (
            <Page title={title}>
              <p>{audience === "staff" ? t("login.staffIntro") : t("login.participantIntro")}</p>
              {!fieldError && <ErrorAlert error={send.error} />}
              <form onSubmit={submit} noValidate>
                <TextField
                  label={t("login.email")}
                  type="email"
                  value={email}
                  onChange={setEmail}
                  autoComplete="email"
                  required
                  error={fieldError}
                />
                <button type="submit" className={ui.button} disabled={send.isPending || !email.trim()}>
                  {send.isPending ? t("login.sending") : t("login.send")}
                </button>
              </form>
            </Page>
          )}
        </div>
        {setup?.demo && (
          <div className={ui.card}>
            <h2>{t("login.demoTitle")}</h2>
            <Alert kind="info">{t("login.demoBody")}</Alert>
            <p>
              <a href="/dev/mail" target="_blank" rel="noopener">
                {t("login.openMail")}
              </a>
            </p>
            {setup.demoLogins && (
              <>
                <h3>{t("login.demoAccounts")}</h3>
                <ul>
                  {Object.entries(setup.demoLogins)
                    .filter(([role]) => (audience === "participant") === (role === "participant"))
                    .map(([role, addr]) => (
                      <li key={role}>
                        <button
                          type="button"
                          className={`${ui.button} ${ui.secondary} ${ui.small}`}
                          onClick={() => setEmail(addr)}
                        >
                          {t(`roles.${role}`, { defaultValue: role })}: {addr}
                        </button>
                      </li>
                    ))}
                </ul>
              </>
            )}
          </div>
        )}
        <p>
          {audience === "staff" ? (
            <Link to="/p/login">{t("login.participantLink")}</Link>
          ) : (
            <Link to="/admin/login">{t("login.staffLink")}</Link>
          )}
        </p>
      </div>
    </main>
  );
}
