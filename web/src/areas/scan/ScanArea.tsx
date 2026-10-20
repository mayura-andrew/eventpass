// SPDX-License-Identifier: Apache-2.0

import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Alert, Page, ui } from "../../components/ui";

export default function ScanArea() {
  const { t } = useTranslation();
  return (
    <main id="main" className={ui.page}>
      <Page title={t("scan.title")}>
        <Alert kind="info">{t("scan.comingSoon")}</Alert>
        <Link to="/admin">{t("admin.nav.label")}</Link>
      </Page>
    </main>
  );
}
