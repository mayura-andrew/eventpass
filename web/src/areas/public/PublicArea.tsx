// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useParams } from "react-router";
import { api, ApiError, unwrap } from "../../api/client";
import { Alert, ErrorAlert, formatDateTime, Loading, Page, ui } from "../../components/ui";

export default function PublicArea() {
  const { t } = useTranslation();
  const { slug = "" } = useParams();
  const q = useQuery({
    queryKey: ["public-event", slug],
    queryFn: () => unwrap(api.GET("/public/events/{slug}", { params: { path: { slug } } })),
  });
  return (
    <main id="main" className={ui.page}>
      {q.isPending && <Loading />}
      {q.error instanceof ApiError && q.error.status === 404 ? (
        <Page title={t("common.notFoundTitle")}>
          <p>{t("public.notFound")}</p>
        </Page>
      ) : (
        <ErrorAlert error={q.error} />
      )}
      {q.data && (
        <Page title={q.data.name}>
          <p className={ui.muted}>{formatDateTime(q.data.startsAt, q.data.timeZone)}</p>
          {q.data.settings?.["description"] && <p>{q.data.settings["description"]}</p>}
          {q.data.settings?.["venue_name"] && (
            <p>
              <strong>{t("public.venue")}:</strong> {q.data.settings["venue_name"]}
              {q.data.settings["venue_address"] ? `, ${q.data.settings["venue_address"]}` : ""}
            </p>
          )}
          <Alert kind="info">{t("public.registrationSoon")}</Alert>
        </Page>
      )}
    </main>
  );
}
