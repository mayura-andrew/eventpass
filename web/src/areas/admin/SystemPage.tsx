// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, unwrap, type Schemas } from "../../api/client";
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
          {q.data.emailCapped !== undefined && (
            <>
              <dt className={ui.label}>{t("admin.system.email")}</dt>
              <dd>{q.data.emailCapped ? t("admin.system.emailCapped") : t("admin.system.emailOk")}</dd>
            </>
          )}
        </dl>
      )}
      {q.data?.sheets && <SheetsHealth sheets={q.data.sheets} />}
    </Page>
  );
}

type Sheets = NonNullable<Schemas["SystemHealth"]["sheets"]>;

function SheetsHealth({ sheets }: { sheets: Sheets }) {
  const { t } = useTranslation();
  const ages = Object.entries(sheets.cacheAgeSeconds).sort(([a], [b]) => a.localeCompare(b));
  return (
    <section aria-labelledby="sheets-h" className={ui.stack}>
      <h2 id="sheets-h">{t("admin.system.sheets")}</h2>
      <dl className={ui.card}>
        <dt className={ui.label}>{t("admin.system.reads")}</dt>
        <dd>{t("admin.system.ofQuota", { used: sheets.readsLastMinute, quota: sheets.quotaPerMinute })}</dd>
        <dt className={ui.label}>{t("admin.system.writes")}</dt>
        <dd>{t("admin.system.ofQuota", { used: sheets.writesLastMinute, quota: sheets.quotaPerMinute })}</dd>
        <dt className={ui.label}>{t("admin.system.retries")}</dt>
        <dd>{sheets.retries}</dd>
        <dt className={ui.label}>{t("admin.system.busy")}</dt>
        <dd>{sheets.busyErrors}</dd>
      </dl>
      <div className={ui.tableWrap}>
        <table className={ui.table}>
          <caption className={ui.muted}>{t("admin.system.cacheCaption", { ttl: sheets.cacheTtlSeconds })}</caption>
          <thead>
            <tr>
              <th scope="col">{t("admin.system.tab")}</th>
              <th scope="col">{t("admin.system.age")}</th>
            </tr>
          </thead>
          <tbody>
            {ages.map(([tab, age]) => (
              <tr key={tab}>
                <td className={ui.mono}>{tab}</td>
                <td>{t("admin.system.seconds", { count: age })}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
