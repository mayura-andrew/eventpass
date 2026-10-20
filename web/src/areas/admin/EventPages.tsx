// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Outlet, useOutletContext, useParams } from "react-router";
import { api, unwrap, type EventSummary } from "../../api/client";
import { ErrorAlert, formatDateTime, Loading, Page, ui } from "../../components/ui";
import s from "./admin.module.css";

type EventContext = { event: EventSummary };

export function useEvent(): EventSummary {
  return useOutletContext<EventContext>().event;
}

export function EventLayout() {
  const { t } = useTranslation();
  const { eventId = "" } = useParams();
  const q = useQuery({
    queryKey: ["event", eventId],
    queryFn: () => unwrap(api.GET("/events/{eventID}", { params: { path: { eventID: eventId } } })),
  });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorAlert error={q.error} />;
  const event = q.data;
  const role = event.role;
  const base = `/admin/events/${event.id}`;
  return (
    <>
      <p className={ui.muted}>
        {formatDateTime(event.startsAt, event.timeZone)} · {t(`eventStatus.${event.status}`)}
      </p>
      <nav aria-label={event.name} className={s.tabs}>
        <NavLink to={base} end>
          {t("admin.event.overview")}
        </NavLink>
        {(role === "admin" || role === "viewer") && (
          <NavLink to={`${base}/participants`}>{t("admin.event.participants")}</NavLink>
        )}
        {role === "admin" && event.sheetConnected && <NavLink to={`${base}/form`}>{t("admin.event.form")}</NavLink>}
        {role === "admin" && <NavLink to={`${base}/staff`}>{t("admin.event.staff")}</NavLink>}
        {role === "admin" && <NavLink to={`${base}/settings`}>{t("admin.event.settings")}</NavLink>}
        {event.status !== "draft" && (
          <a href={`/e/${event.slug}`} target="_blank" rel="noopener">
            {t("admin.event.publicPage")}
          </a>
        )}
      </nav>
      <Outlet context={{ event } satisfies EventContext} />
    </>
  );
}

export function EventOverview() {
  const { t } = useTranslation();
  const event = useEvent();
  const canStats = event.role === "admin" || event.role === "viewer";
  const path = { params: { path: { eventID: event.id } } };
  // Polling keeps the dashboard within 5 s of reality until the SSE stream
  // lands in M2.
  const stats = useQuery({
    queryKey: ["stats", event.id],
    queryFn: () => unwrap(api.GET("/events/{eventID}/stats", path)),
    enabled: canStats && !!event.sheetConnected,
    refetchInterval: 5000,
  });
  const activity = useQuery({
    queryKey: ["activity", event.id],
    queryFn: () => unwrap(api.GET("/events/{eventID}/activity", path)),
    enabled: canStats && !!event.sheetConnected,
    refetchInterval: 5000,
  });

  if (!canStats) {
    return (
      <Page title={event.name}>
        <p>{t("scan.comingSoon")}</p>
      </Page>
    );
  }
  const total = stats.data?.total ?? 0;
  if (!event.sheetConnected) {
    return (
      <Page title={event.name}>
        <p className={`${ui.card} ${ui.empty}`}>
          {t("admin.event.connectFirst")}{" "}
          <Link to={`/admin/events/${event.id}/settings`}>{t("admin.event.settings")}</Link>
        </p>
      </Page>
    );
  }
  return (
    <Page title={event.name}>
      <ErrorAlert error={stats.error ?? activity.error} />
      {stats.isPending && <Loading />}
      {stats.data && (
        <section aria-labelledby="stats-h">
          <h2 id="stats-h">{t("admin.event.stats")}</h2>
          <div className={s.stats}>
            <div className={`${ui.card} ${s.stat}`}>
              <span className={s.statValue}>{total}</span>
              <span className={ui.muted}>{t("admin.event.total")}</span>
            </div>
            {Object.entries(stats.data.byStatus).map(([status, n]) => (
              <div key={status} className={`${ui.card} ${s.stat}`}>
                <span className={s.statValue}>{n}</span>
                <span className={ui.muted}>{t(`status.${status}`)}</span>
              </div>
            ))}
          </div>
        </section>
      )}
      <div className={s.columns}>
        {stats.data && (
          <section className={ui.card} aria-labelledby="cp-h">
            <h2 id="cp-h">{t("admin.event.checkpoints")}</h2>
            <ul className={s.list}>
              {stats.data.checkpoints.map((cp) => {
                const pct = total ? Math.round((cp.done / total) * 100) : 0;
                return (
                  <li key={cp.id}>
                    <div className={ui.spread}>
                      <strong>{cp.name}</strong>
                      <span>{t("admin.event.doneOf", { done: cp.done, total })}</span>
                    </div>
                    <div
                      className={s.bar}
                      role="progressbar"
                      aria-label={cp.name}
                      aria-valuemin={0}
                      aria-valuemax={total}
                      aria-valuenow={cp.done}
                    >
                      <Fill pct={pct} />
                    </div>
                  </li>
                );
              })}
            </ul>
          </section>
        )}
        <section className={ui.card} aria-labelledby="act-h">
          <h2 id="act-h">{t("admin.event.activity")}</h2>
          {activity.data && activity.data.items.length === 0 && (
            <p className={ui.muted}>{t("admin.event.noActivity")}</p>
          )}
          <ul className={s.list}>
            {activity.data?.items.slice(0, 15).map((a, i) => (
              <li key={`${a.time}-${i}`}>
                <span className={ui.muted}>{formatDateTime(a.time, event.timeZone)}</span>
                <br />
                {a.actor && <strong>{a.actor} </strong>}
                <span className={ui.mono}>{a.action}</span>
                {a.result && <span> · {a.result}</span>}
              </li>
            ))}
          </ul>
        </section>
      </div>
    </Page>
  );
}

/** Width via the CSSOM (allowed by the CSP), not an inline style attribute. */
function Fill({ pct }: { pct: number }) {
  return <div className={s.barFill} ref={(el) => el?.style.setProperty("width", `${pct}%`)} />;
}
