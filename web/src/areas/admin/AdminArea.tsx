// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, NavLink, Outlet, Route, Routes, useNavigate } from "react-router";
import { NotFound } from "../../App";
import { api, unwrap } from "../../api/client";
import { LoginPage } from "../../components/LoginPage";
import { ErrorAlert, Loading, ui } from "../../components/ui";
import { useMe, useSetup } from "../../session";
import s from "./admin.module.css";
import { AuditPage } from "./AuditPage";
import { EventLayout, EventOverview } from "./EventPages";
import { EventSettingsPage } from "./EventSettingsPage";
import { EventsPage } from "./EventsPage";
import { FormPage } from "./FormPage";
import { ParticipantsPage } from "./ParticipantsPage";
import { SettingsPage } from "./SettingsPage";
import { SetupWizard } from "./SetupWizard";
import { StaffPage } from "./StaffPage";
import { SystemPage } from "./SystemPage";
import { UsersPage } from "./UsersPage";

export default function AdminArea() {
  return (
    <Routes>
      <Route path="setup" element={<SetupWizard />} />
      <Route
        path="login"
        element={
          <NeedsSetup>
            <LoginPage audience="staff" />
          </NeedsSetup>
        }
      />
      <Route
        element={
          <NeedsSetup>
            <Shell />
          </NeedsSetup>
        }
      >
        <Route index element={<EventsPage />} />
        <Route path="events/:eventId" element={<EventLayout />}>
          <Route index element={<EventOverview />} />
          <Route path="participants" element={<ParticipantsPage />} />
          <Route path="form" element={<FormPage />} />
          <Route path="settings" element={<EventSettingsPage />} />
          <Route path="staff" element={<StaffPage />} />
        </Route>
        <Route path="users" element={<UsersPage />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="audit" element={<AuditPage />} />
        <Route path="system" element={<SystemPage />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

/** Sends a fresh install to the setup wizard before anything else. */
function NeedsSetup({ children }: { children: ReactNode }) {
  const setup = useSetup();
  if (setup.isPending) return <Loading />;
  if (setup.data && !setup.data.complete && !setup.data.demo) return <Navigate to="/admin/setup" replace />;
  return <>{children}</>;
}

/** Hook for pages: the signed-in user's workspace permissions. */
export function useCan() {
  const { data } = useMe();
  const perms = new Set(data?.workspacePermissions ?? []);
  return (perm: string) => perms.has(perm);
}

function Shell() {
  const { t } = useTranslation();
  const me = useMe();
  const { data: setup } = useSetup();
  const can = useCan();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const logout = useMutation({
    mutationFn: () => unwrap(api.POST("/auth/logout")),
    onSuccess: () => {
      qc.clear();
      navigate("/admin/login");
    },
  });

  if (me.isPending) return <Loading />;
  if (me.error) return <ErrorAlert error={me.error} />;
  if (!me.data) return <Navigate to="/admin/login" replace />;
  if (me.data.kind === "participant") return <Navigate to="/p" replace />;

  return (
    <div className={s.shell}>
      <a className="skip-link" href="#main">
        {t("common.skipToContent")}
      </a>
      <header className={s.header}>
        <NavLink to="/admin" end className={s.brand}>
          {setup?.productName ?? "EventPass"}
        </NavLink>
        <nav aria-label={t("admin.nav.label")} className={s.nav}>
          <NavLink to="/admin" end>
            {t("admin.nav.events")}
          </NavLink>
          {can("users.read") && <NavLink to="/admin/users">{t("admin.nav.users")}</NavLink>}
          {can("workspace.read") && <NavLink to="/admin/settings">{t("admin.nav.settings")}</NavLink>}
          {can("audit.read") && <NavLink to="/admin/audit">{t("admin.nav.audit")}</NavLink>}
          {can("system.health") && <NavLink to="/admin/system">{t("admin.nav.system")}</NavLink>}
        </nav>
        <div className={s.user}>
          <span>{me.data.email}</span>
          <button type="button" className={`${ui.button} ${ui.secondary} ${ui.small}`} onClick={() => logout.mutate()}>
            {t("common.signOut")}
          </button>
        </div>
      </header>
      <main id="main" className={s.main}>
        <Outlet />
      </main>
    </div>
  );
}
