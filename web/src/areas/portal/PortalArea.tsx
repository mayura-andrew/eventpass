// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Navigate, Route, Routes, useNavigate } from "react-router";
import { NotFound } from "../../App";
import { api, unwrap } from "../../api/client";
import { LoginPage } from "../../components/LoginPage";
import { Alert, ErrorAlert, formatDateTime, Loading, Page, StatusBadge, ui } from "../../components/ui";
import { useMe, useSetup } from "../../session";

export default function PortalArea() {
  return (
    <Routes>
      <Route path="login" element={<LoginPage audience="participant" />} />
      <Route index element={<MyEvents />} />
      <Route path="*" element={<NotFound />} />
    </Routes>
  );
}

function MyEvents() {
  const { t } = useTranslation();
  const me = useMe();
  const { data: setup } = useSetup();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const isParticipant = me.data?.kind === "participant";
  const regs = useQuery({
    queryKey: ["my-registrations"],
    queryFn: () => unwrap(api.GET("/me/registrations")),
    enabled: isParticipant,
  });
  const logout = useMutation({
    mutationFn: () => unwrap(api.POST("/auth/logout")),
    onSuccess: () => {
      qc.clear();
      navigate("/p/login");
    },
  });

  if (me.isPending) return <Loading />;
  if (!isParticipant) return <Navigate to="/p/login" replace />;

  return (
    <main id="main" className={ui.page}>
      <div className={ui.spread}>
        <span className={ui.muted}>{setup?.productName}</span>
        <button type="button" className={`${ui.button} ${ui.secondary} ${ui.small}`} onClick={() => logout.mutate()}>
          {t("common.signOut")}
        </button>
      </div>
      <Page title={t("portal.title")}>
        <ErrorAlert error={regs.error} />
        {regs.isPending && <Loading />}
        {regs.data && regs.data.registrations.length === 0 && (
          <p className={`${ui.card} ${ui.empty}`}>{t("portal.empty", { email: me.data?.email })}</p>
        )}
        <div className={ui.stack}>
          {regs.data?.registrations.map(({ event, participant }) => (
            <article key={event.id} className={`${ui.card} ${ui.stack}`}>
              <div>
                <h2>{event.name}</h2>
                <p className={ui.muted}>{formatDateTime(event.startsAt, event.timeZone)}</p>
              </div>
              <div className={ui.row}>
                <StatusBadge status={participant.status} />
                <span>
                  {t("portal.passId")}: <strong className={ui.mono}>{participant.passId}</strong>
                </span>
              </div>
              <Alert kind="info">{t("portal.comingSoon")}</Alert>
            </article>
          ))}
        </div>
      </Page>
    </main>
  );
}
