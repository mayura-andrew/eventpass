// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, unwrap } from "../../api/client";
import { ErrorAlert, formatDateTime, Loading, Page, ui } from "../../components/ui";

export function AuditPage() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["audit"],
    queryFn: () => unwrap(api.GET("/workspace/audit", { params: { query: { limit: 200 } } })),
  });
  return (
    <Page title={t("admin.audit.title")}>
      <ErrorAlert error={q.error} />
      {q.isPending && <Loading />}
      {q.data && q.data.entries.length === 0 && <p className={`${ui.card} ${ui.empty}`}>{t("admin.audit.empty")}</p>}
      {q.data && q.data.entries.length > 0 && (
        <div className={ui.tableWrap}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">{t("admin.audit.time")}</th>
                <th scope="col">{t("admin.audit.actor")}</th>
                <th scope="col">{t("admin.audit.action")}</th>
                <th scope="col">{t("admin.audit.target")}</th>
              </tr>
            </thead>
            <tbody>
              {q.data.entries.map((e, i) => (
                <tr key={`${e.time}-${i}`}>
                  <td>{formatDateTime(e.time)}</td>
                  <td>{e.actorEmail ?? "—"}</td>
                  <td className={ui.mono}>{e.action}</td>
                  <td className={ui.mono}>
                    {e.targetId}
                    {e.details ? ` (${e.details})` : ""}
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
