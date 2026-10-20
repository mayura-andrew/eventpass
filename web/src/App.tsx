// SPDX-License-Identifier: Apache-2.0

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { lazy, Suspense, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { BrowserRouter, Link, Navigate, Route, Routes } from "react-router";
import { ApiError } from "./api/client";
import { Loading, Page, ui } from "./components/ui";
import { VerifyPage } from "./components/VerifyPage";
import { useSetup } from "./session";
import { applyTheme } from "./theme";

// Each area is its own chunk: participants never download admin code.
const AdminArea = lazy(() => import("./areas/admin/AdminArea"));
const PortalArea = lazy(() => import("./areas/portal/PortalArea"));
const PublicArea = lazy(() => import("./areas/public/PublicArea"));
const ScanArea = lazy(() => import("./areas/scan/ScanArea"));

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
      refetchOnWindowFocus: false,
    },
  },
});

function Branding() {
  const { data } = useSetup();
  useEffect(() => {
    if (!data) return;
    applyTheme({ primary_color: data.primaryColor ?? "", accent_color: data.accentColor ?? "" });
    document.documentElement.dataset["product"] = data.productName;
  }, [data]);
  return null;
}

export function NotFound() {
  const { t } = useTranslation();
  return (
    <main id="main" className={ui.centered}>
      <div className={ui.narrow}>
        <Page title={t("common.notFoundTitle")}>
          <p>{t("common.notFoundBody")}</p>
          <Link to="/">{t("common.goHome")}</Link>
        </Page>
      </div>
    </main>
  );
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Branding />
      <BrowserRouter>
        <Suspense fallback={<Loading />}>
          <Routes>
            <Route path="/" element={<Navigate to="/admin" replace />} />
            <Route path="/login/verify" element={<VerifyPage />} />
            <Route path="/admin/*" element={<AdminArea />} />
            <Route path="/p/*" element={<PortalArea />} />
            <Route path="/e/:slug/*" element={<PublicArea />} />
            <Route path="/scan/*" element={<ScanArea />} />
            <Route path="*" element={<NotFound />} />
          </Routes>
        </Suspense>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
