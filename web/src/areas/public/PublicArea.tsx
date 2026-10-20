// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useParams } from "react-router";
import { api, ApiError, unwrap, type Schemas } from "../../api/client";
import { missingRequired, ProfileForm } from "../../components/ProfileForm";
import { Alert, ErrorAlert, formatDateTime, Loading, Page, ui } from "../../components/ui";
import { useSetup } from "../../session";
import s from "./public.module.css";

type PublicEvent = Schemas["PublicEvent"];
type RegisterStatus = Schemas["RegisterResult"]["status"];

export default function PublicArea() {
  const { t } = useTranslation();
  const { slug = "" } = useParams();
  const { data: setup } = useSetup();
  const q = useQuery({
    queryKey: ["public-event", slug],
    queryFn: () => unwrap(api.GET("/public/events/{slug}", { params: { path: { slug } } })),
  });
  const [done, setDone] = useState<RegisterStatus | null>(null);
  return (
    <main id="main" className={ui.page}>
      <p className={ui.muted}>{setup?.productName}</p>
      {q.isPending && <Loading />}
      {q.error instanceof ApiError && q.error.status === 404 ? (
        <Page title={t("common.notFoundTitle")}>
          <p>{t("public.notFound")}</p>
        </Page>
      ) : (
        <ErrorAlert error={q.error} />
      )}
      {q.data && !done && (
        <Page title={q.data.name}>
          <EventInfo event={q.data} />
          <Registration event={q.data} slug={slug} onDone={setDone} />
        </Page>
      )}
      {q.data && done && <Confirmation event={q.data} status={done} />}
    </main>
  );
}

function EventInfo({ event }: { event: PublicEvent }) {
  const { t } = useTranslation();
  const settings = event.settings ?? {};
  return (
    <section className={ui.stack} aria-label={t("public.about")}>
      <p>
        <strong>{formatDateTime(event.startsAt, event.timeZone)}</strong>
        {event.endsAt && <> – {formatDateTime(event.endsAt, event.timeZone)}</>}
      </p>
      {settings["venue_name"] && (
        <p>
          <strong>{t("public.venue")}:</strong> {settings["venue_name"]}
          {settings["venue_address"] ? `, ${settings["venue_address"]}` : ""}
        </p>
      )}
      {settings["description"] && <p className={s.description}>{settings["description"]}</p>}
    </section>
  );
}

function Registration({
  event,
  slug,
  onDone,
}: {
  event: PublicEvent;
  slug: string;
  onDone: (s: RegisterStatus) => void;
}) {
  const { t, i18n } = useTranslation();
  const [values, setValues] = useState<Record<string, string>>({});
  const [consent, setConsent] = useState(false);
  const [localErrs, setLocalErrs] = useState<Record<string, string>>({});
  const register = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/public/events/{slug}/register", {
          params: { path: { slug } },
          body: { values, consent, locale: i18n.language },
        }),
      ),
    onSuccess: (r) => {
      onDone(r.status);
      window.scrollTo(0, 0);
    },
  });

  if (event.registration !== "open") {
    return <Alert kind="info">{t(`public.reg.${event.registration}`)}</Alert>;
  }
  const serverErrs = register.error instanceof ApiError ? register.error.fieldErrors() : {};
  const errs = { ...serverErrs, ...localErrs };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const missing = missingRequired(event.fields, values);
    if (event.consentText && !consent) missing["consent"] = "required";
    setLocalErrs(missing);
    if (Object.keys(missing).length > 0) {
      // Move focus to the first problem so keyboard and screen-reader users
      // land on it.
      requestAnimationFrame(() => document.querySelector<HTMLElement>("[aria-invalid='true']")?.focus());
      return;
    }
    register.mutate();
  };
  return (
    <form className={`${ui.card} ${s.form}`} onSubmit={submit} noValidate aria-labelledby="reg-h">
      <h2 id="reg-h">{t("public.register")}</h2>
      {event.spotsLeft >= 0 && <p className={ui.muted}>{t("public.spotsLeft", { count: event.spotsLeft })}</p>}
      {Object.keys(serverErrs).length === 0 && <ErrorAlert error={register.error} />}
      {Object.keys(errs).length > 0 && <Alert kind="warn">{t("public.fixErrors")}</Alert>}
      <ProfileForm
        fields={event.fields}
        values={values}
        errors={errs}
        onChange={(k, v) => {
          setValues((cur) => ({ ...cur, [k]: v }));
          setLocalErrs((cur) => Object.fromEntries(Object.entries(cur).filter(([key]) => key !== k)));
        }}
      />
      {event.consentText && (
        <div className={ui.field}>
          <p className={s.consentText}>{event.consentText}</p>
          <label className={s.consent}>
            <input
              type="checkbox"
              checked={consent}
              onChange={(e) => setConsent(e.target.checked)}
              aria-invalid={errs["consent"] ? true : undefined}
              aria-describedby={errs["consent"] ? "consent-err" : undefined}
            />
            {t("public.consent")}
          </label>
          {errs["consent"] && (
            <span id="consent-err" className={ui.fieldError}>
              {t("public.consentRequired")}
            </span>
          )}
        </div>
      )}
      <button type="submit" className={`${ui.button} ${s.submit}`} disabled={register.isPending}>
        {register.isPending ? t("public.registering") : t("public.register")}
      </button>
    </form>
  );
}

function Confirmation({ event, status }: { event: PublicEvent; status: RegisterStatus }) {
  const { t } = useTranslation();
  return (
    <Page title={t(`public.done.${status}.title`)}>
      <div className={`${ui.card} ${ui.stack}`}>
        <p>{t(`public.done.${status}.body`, { event: event.name })}</p>
        <Alert kind="info">{t("public.checkSpam")}</Alert>
        <p>
          <a href="/p/login">{t("public.openPortal")}</a>
        </p>
      </div>
    </Page>
  );
}
