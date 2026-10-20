// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, unwrap } from "../../api/client";
import { ErrorAlert, Loading, Page, ui } from "../../components/ui";

function duration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return h ? `${h} h ${m} min` : `${m} min`;
}

export function SystemPage() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["health"],
    queryFn: () => unwrap(api.GET("/system/health")),
    refetchInterval: 15000,
  });
  return (
    <Page title={t("admin.system.title")}>
      <ErrorAlert error={q.error} />
      {q.isPending && <Loading />}
      {q.data && (
        <dl className={ui.card}>
          <dt className={ui.label}>{t("admin.system.version")}</dt>
          <dd>{q.data.version}</dd>
          <dt className={ui.label}>{t("admin.system.storage")}</dt>
          <dd>{q.data.storage}</dd>
          <dt className={ui.label}>{t("admin.system.demo")}</dt>
          <dd>{q.data.demo ? "✓" : "—"}</dd>
          <dt className={ui.label}>{t("admin.system.uptime")}</dt>
          <dd>{duration(q.data.uptimeSeconds)}</dd>
        </dl>
      )}
    </Page>
  );
}
