// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api, unwrap } from "../api/client";
import { ErrorAlert, Loading, Page, ui } from "./ui";

/**
 * The emailed link opens this page, which POSTs the token (ADR 0003): mail
 * scanners that prefetch links don't run scripts, so they can't burn it.
 */
export function VerifyPage() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [error, setError] = useState<unknown>(null);
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return; // StrictMode runs effects twice; the token works once
    started.current = true;
    const token = params.get("token") ?? "";
    // Drop the token from the address bar and history straight away.
    window.history.replaceState(null, "", "/login/verify");
    unwrap(api.POST("/auth/callback", { body: { token } }))
      .then(async (res) => {
        await qc.invalidateQueries({ queryKey: ["me"] });
        navigate(res.redirect, { replace: true });
      })
      .catch(setError);
  }, [params, navigate, qc]);

  return (
    <main id="main" className={ui.centered}>
      <div className={ui.narrow}>
        {error ? (
          <Page title={t("login.verifyFailed")}>
            <ErrorAlert error={error} />
            <p className={ui.row}>
              <Link to="/admin/login">{t("login.staffLink")}</Link>
            </p>
            <p className={ui.row}>
              <Link to="/p/login">{t("login.participantLink")}</Link>
            </p>
          </Page>
        ) : (
          <Page title={t("login.verifying")}>
            <Loading />
          </Page>
        )}
      </div>
    </main>
  );
}
