// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { api, ApiError, unwrap } from "../../api/client";
import { ErrorAlert, formatDateTime, Loading, Page, TextField, ui } from "../../components/ui";
import s from "./admin.module.css";
import { useCan } from "./AdminArea";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64);
}

export function EventsPage() {
  const { t } = useTranslation();
  const can = useCan();
  const events = useQuery({ queryKey: ["events"], queryFn: () => unwrap(api.GET("/events")) });
  const [creating, setCreating] = useState(false);

  return (
    <Page
      title={t("admin.events.title")}
      actions={
        can("event.create") && !creating ? (
          <button type="button" className={ui.button} onClick={() => setCreating(true)}>
            {t("admin.events.create")}
          </button>
        ) : null
      }
    >
      {creating && <CreateEvent onCancel={() => setCreating(false)} />}
      {events.isPending && <Loading />}
      <ErrorAlert error={events.error} />
      {events.data && events.data.events.length === 0 && (
        <p className={`${ui.card} ${ui.empty}`}>
          {can("event.create") ? t("admin.events.empty") : t("admin.events.noAccess")}
        </p>
      )}
      <div className={s.grid}>
        {events.data?.events.map((e) => (
          <Link key={e.id} to={`/admin/events/${e.id}`} className={`${ui.card} ${s.eventCard}`}>
            <h2>{e.name}</h2>
            <span className={ui.muted}>{formatDateTime(e.startsAt, e.timeZone)}</span>
            <span className={ui.row}>
              <span className={`${ui.badge} ${ui.badgeNeutral}`}>{t(`eventStatus.${e.status}`)}</span>
              {e.role && (
                <span className={`${ui.badge} ${ui.badgeInfo}`}>
                  {t("admin.events.yourRole")}: {t(`roles.${e.role}`)}
                </span>
              )}
            </span>
          </Link>
        ))}
      </div>
    </Page>
  );
}

function CreateEvent({ onCancel }: { onCancel: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const create = useMutation({
    mutationFn: () => unwrap(api.POST("/events", { body: { name, slug } })),
    onSuccess: async (e) => {
      await qc.invalidateQueries({ queryKey: ["events"] });
      await qc.invalidateQueries({ queryKey: ["me"] });
      navigate(`/admin/events/${e.id}`);
    },
  });
  const errs = create.error instanceof ApiError ? create.error.fieldErrors() : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  return (
    <form className={`${ui.card} ${ui.stack}`} onSubmit={submit} noValidate>
      <h2>{t("admin.events.create")}</h2>
      {Object.keys(errs).length === 0 && <ErrorAlert error={create.error} />}
      <div>
        <TextField
          label={t("admin.events.name")}
          value={name}
          maxLength={120}
          required
          error={errs["name"]}
          onChange={(v) => {
            setName(v);
            if (!slugEdited) setSlug(slugify(v));
          }}
        />
        <TextField
          label={t("admin.events.slug")}
          hint={t("admin.events.slugHint", { slug: slug || "…" })}
          value={slug}
          required
          error={errs["slug"]}
          onChange={(v) => {
            setSlugEdited(true);
            setSlug(v);
          }}
        />
      </div>
      <div className={ui.row}>
        <button type="submit" className={ui.button} disabled={create.isPending}>
          {create.isPending ? t("common.saving") : t("admin.events.create")}
        </button>
        <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
