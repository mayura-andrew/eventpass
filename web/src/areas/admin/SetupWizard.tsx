// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Navigate } from "react-router";
import { api, ApiError, unwrap, type Schemas } from "../../api/client";
import { Alert, ErrorAlert, Loading, Page, SelectField, TextField, ui } from "../../components/ui";
import { useSetup } from "../../session";
import s from "./admin.module.css";

type Step = "token" | "google" | "smtp" | "workspace" | "done";
const steps: Step[] = ["token", "google", "smtp", "workspace", "done"];

/** SMTP presets: host and port; everything else the organizer types. */
export const smtpPresets: Record<string, { host: string; port: number }> = {
  gmail: { host: "smtp.gmail.com", port: 587 },
  workspace: { host: "smtp.gmail.com", port: 587 },
  brevo: { host: "smtp-relay.brevo.com", port: 587 },
  mailgun: { host: "smtp.mailgun.org", port: 587 },
  ses: { host: "email-smtp.us-east-1.amazonaws.com", port: 587 },
  custom: { host: "", port: 587 },
};

const tokenHeader = (token: string) => ({ params: { header: { "X-Setup-Token": token } } });

export function SetupWizard() {
  const { t } = useTranslation();
  const setup = useSetup();
  const [step, setStep] = useState<Step>("token");
  const [token, setToken] = useState("");
  const [google, setGoogle] = useState<Schemas["GoogleInput"]>({ credentialsJson: "", spreadsheet: "" });
  const [smtp, setSmtp] = useState<Schemas["SMTPInput"]>({
    ...smtpPresets["gmail"],
    username: "",
    password: "",
    from: "",
    testTo: "",
  });
  const [ownerEmail, setOwnerEmail] = useState("");

  if (setup.isPending) return <Loading />;
  if (setup.error) return <ErrorAlert error={setup.error} />;
  if ((setup.data.complete || setup.data.demo) && step !== "done") return <Navigate to="/admin" replace />;
  const state = setup.data.setup;
  const n = steps.indexOf(step);

  return (
    <main id="main" className={ui.page}>
      <p className={ui.muted}>{t("setup.progress", { step: Math.min(n + 1, 4), total: 4 })}</p>
      {step === "token" && (
        <TokenStep
          token={token}
          setToken={setToken}
          onNext={() => setStep(state?.googleConfigured && state.sheetConfigured ? "smtp" : "google")}
        />
      )}
      {step === "google" && (
        <GoogleStep
          token={token}
          value={google}
          onChange={setGoogle}
          keyFromEnv={!!state?.googleConfigured}
          serviceAccountEmail={state?.serviceAccountEmail}
          onBack={() => setStep("token")}
          onNext={() => setStep("smtp")}
        />
      )}
      {step === "smtp" && (
        <SMTPStep
          token={token}
          value={smtp}
          onChange={setSmtp}
          fromEnv={!!state?.smtpConfigured}
          onBack={() => setStep("google")}
          onNext={() => setStep("workspace")}
        />
      )}
      {step === "workspace" && (
        <WorkspaceStep
          token={token}
          google={google}
          smtp={smtp}
          onBack={() => setStep("smtp")}
          onDone={(email) => {
            setOwnerEmail(email);
            setStep("done");
          }}
        />
      )}
      {step === "done" && (
        <Page title={t("setup.done.title")}>
          <div className={`${ui.card} ${ui.stack}`}>
            <p>{t("setup.done.body", { email: ownerEmail })}</p>
            <Alert kind="info">{t("public.checkSpam")}</Alert>
            <a className={ui.button} href="/admin/login">
              {t("setup.done.signIn")}
            </a>
          </div>
        </Page>
      )}
    </main>
  );
}

function StepForm({
  title,
  intro,
  children,
  error,
  busy,
  submitLabel,
  onSubmit,
  onBack,
}: {
  title: string;
  intro?: ReactNode;
  children: ReactNode;
  error?: unknown;
  busy?: boolean;
  submitLabel: string;
  onSubmit: () => void;
  onBack?: () => void;
}) {
  const { t } = useTranslation();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSubmit();
  };
  return (
    <Page title={title}>
      <form className={`${ui.card} ${ui.stack}`} onSubmit={submit} noValidate>
        {intro}
        <ErrorAlert error={error} />
        <div>{children}</div>
        <div className={ui.row}>
          <button type="submit" className={ui.button} disabled={busy}>
            {busy ? t("setup.checking") : submitLabel}
          </button>
          {onBack && (
            <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={onBack}>
              {t("common.previous")}
            </button>
          )}
        </div>
      </form>
    </Page>
  );
}

function TokenStep({ token, setToken, onNext }: { token: string; setToken: (v: string) => void; onNext: () => void }) {
  const { t } = useTranslation();
  // The token is checked by the first real call; an empty one is caught here.
  const [err, setErr] = useState<string>();
  return (
    <StepForm
      title={t("setup.welcome.title")}
      intro={<p>{t("setup.welcome.body")}</p>}
      submitLabel={t("common.next")}
      onSubmit={() => (token.trim() ? onNext() : setErr("required"))}
    >
      <TextField
        label={t("setup.welcome.token")}
        hint={t("setup.welcome.tokenHint")}
        value={token}
        onChange={(v) => {
          setToken(v.trim());
          setErr(undefined);
        }}
        error={err}
        autoComplete="off"
        required
      />
    </StepForm>
  );
}

function GoogleStep({
  token,
  value,
  onChange,
  keyFromEnv,
  serviceAccountEmail,
  onBack,
  onNext,
}: {
  token: string;
  value: Schemas["GoogleInput"];
  onChange: (v: Schemas["GoogleInput"]) => void;
  keyFromEnv: boolean;
  serviceAccountEmail?: string | undefined;
  onBack: () => void;
  onNext: () => void;
}) {
  const { t } = useTranslation();
  const check = useMutation({
    mutationFn: () => unwrap(api.POST("/setup/google-check", { body: value, ...tokenHeader(token) })),
  });
  const email = check.data?.serviceAccountEmail || serviceAccountEmail || emailFromKey(value.credentialsJson ?? "");
  return (
    <StepForm
      title={t("setup.google.title")}
      intro={
        <ol className={ui.stack}>
          <li>{t("setup.google.step1")}</li>
          <li>{t("setup.google.step2")}</li>
          <li>{t("setup.google.step3")}</li>
        </ol>
      }
      error={check.error}
      busy={check.isPending}
      submitLabel={check.isSuccess ? t("common.next") : t("setup.google.check")}
      onSubmit={() => (check.isSuccess ? onNext() : check.mutate())}
      onBack={onBack}
    >
      {keyFromEnv ? (
        <Alert kind="info">{t("setup.google.keyFromEnv")}</Alert>
      ) : (
        <div className={ui.field}>
          <label className={ui.label}>
            {t("setup.google.key")}
            <span className={`${ui.hint} ${s.block}`}>{t("setup.google.keyHint")}</span>
            <textarea
              className={`${ui.input} ${s.textarea} ${ui.mono}`}
              value={value.credentialsJson}
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => {
                check.reset();
                onChange({ ...value, credentialsJson: e.target.value });
              }}
            />
          </label>
        </div>
      )}
      {email && <ShareWith email={email} />}
      <TextField
        label={t("setup.google.sheet")}
        hint={t("setup.google.sheetHint")}
        placeholder="https://docs.google.com/spreadsheets/d/…"
        value={value.spreadsheet ?? ""}
        onChange={(v) => {
          check.reset();
          onChange({ ...value, spreadsheet: v });
        }}
        required
      />
      {check.data && <Alert kind="ok">{t("setup.google.ok", { title: check.data.spreadsheetTitle })}</Alert>}
    </StepForm>
  );
}

/** Reads client_email from a pasted key so the share step can show it early. */
export function emailFromKey(json: string): string {
  try {
    const v = JSON.parse(json) as { client_email?: unknown };
    return typeof v.client_email === "string" ? v.client_email : "";
  } catch {
    return "";
  }
}

function ShareWith({ email }: { email: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  return (
    <div className={ui.field}>
      <span className={ui.label}>{t("setup.google.shareWith")}</span>
      <div className={ui.row}>
        <span className={s.code}>{email}</span>
        <button
          type="button"
          className={`${ui.button} ${ui.secondary} ${ui.small}`}
          onClick={() => void navigator.clipboard?.writeText(email).then(() => setCopied(true))}
        >
          {copied ? t("common.copied") : t("common.copy")}
        </button>
      </div>
    </div>
  );
}

function SMTPStep({
  token,
  value,
  onChange,
  fromEnv,
  onBack,
  onNext,
}: {
  token: string;
  value: Schemas["SMTPInput"];
  onChange: (v: Schemas["SMTPInput"]) => void;
  fromEnv: boolean;
  onBack: () => void;
  onNext: () => void;
}) {
  const { t } = useTranslation();
  const [preset, setPreset] = useState(fromEnv ? "env" : "gmail");
  const body = preset === "env" ? { testTo: value.testTo ?? "" } : value;
  const check = useMutation({
    mutationFn: () => unwrap(api.POST("/setup/smtp-check", { body, ...tokenHeader(token) })),
  });
  const errs = check.error instanceof ApiError ? check.error.fieldErrors() : {};
  const set = (patch: Partial<Schemas["SMTPInput"]>) => {
    check.reset();
    onChange({ ...value, ...patch });
  };
  return (
    <StepForm
      title={t("setup.smtp.title")}
      intro={<p>{t("setup.smtp.intro")}</p>}
      error={Object.keys(errs).length ? undefined : check.error}
      busy={check.isPending}
      submitLabel={check.isSuccess ? t("common.next") : t("setup.smtp.send")}
      onSubmit={() => (check.isSuccess ? onNext() : check.mutate())}
      onBack={onBack}
    >
      <SelectField
        label={t("setup.smtp.provider")}
        value={preset}
        onChange={(p) => {
          setPreset(p);
          const pr = smtpPresets[p];
          if (pr) set({ host: pr.host, port: pr.port });
          else set({ host: "" });
        }}
        options={[
          ...(fromEnv ? [{ value: "env", label: t("setup.smtp.env") }] : []),
          ...Object.keys(smtpPresets).map((k) => ({ value: k, label: t(`setup.smtp.presets.${k}`) })),
        ]}
      />
      {preset === "gmail" && <Alert kind="info">{t("setup.smtp.gmailHint")}</Alert>}
      {preset !== "env" && (
        <>
          <TextField
            label={t("setup.smtp.host")}
            value={value.host ?? ""}
            onChange={(v) => set({ host: v })}
            required
          />
          <TextField
            label={t("setup.smtp.port")}
            type="number"
            value={String(value.port ?? "")}
            onChange={(v) => set({ port: Number(v) || 0 })}
          />
          <TextField
            label={t("setup.smtp.username")}
            value={value.username ?? ""}
            onChange={(v) => set({ username: v })}
            autoComplete="off"
          />
          <TextField
            label={t("setup.smtp.password")}
            type="password"
            value={value.password ?? ""}
            onChange={(v) => set({ password: v })}
            autoComplete="new-password"
          />
          <TextField
            label={t("setup.smtp.from")}
            hint={t("setup.smtp.fromHint")}
            value={value.from ?? ""}
            onChange={(v) => set({ from: v })}
            required
          />
        </>
      )}
      <TextField
        label={t("setup.smtp.testTo")}
        type="email"
        value={value.testTo ?? ""}
        onChange={(v) => set({ testTo: v })}
        error={errs["testTo"]}
        required
      />
      {check.isSuccess && <Alert kind="ok">{t("setup.smtp.ok", { email: value.testTo })}</Alert>}
    </StepForm>
  );
}

function WorkspaceStep({
  token,
  google,
  smtp,
  onBack,
  onDone,
}: {
  token: string;
  google: Schemas["GoogleInput"];
  smtp: Schemas["SMTPInput"];
  onBack: () => void;
  onDone: (email: string) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [productName, setProductName] = useState("EventPass");
  const [timeZone, setTimeZone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
  const [primaryColor, setPrimaryColor] = useState("#1B4DDB");
  const [ownerEmail, setOwnerEmail] = useState(smtp.testTo ?? "");
  const [ownerName, setOwnerName] = useState("");
  const complete = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/setup/complete", {
          body: { google, smtp, productName, timeZone, primaryColor, ownerEmail, ownerName },
          ...tokenHeader(token),
        }),
      ),
    onSuccess: (r) => {
      onDone(r.ownerEmail);
      void qc.invalidateQueries({ queryKey: ["setup"] });
    },
  });
  const errs = complete.error instanceof ApiError ? complete.error.fieldErrors() : {};
  return (
    <StepForm
      title={t("setup.workspace.title")}
      error={Object.keys(errs).length ? undefined : complete.error}
      busy={complete.isPending}
      submitLabel={t("setup.workspace.finish")}
      onSubmit={() => complete.mutate()}
      onBack={onBack}
    >
      <TextField
        label={t("setup.workspace.productName")}
        hint={t("setup.workspace.productNameHint")}
        value={productName}
        onChange={setProductName}
        error={errs["productName"]}
        maxLength={80}
        required
      />
      <TextField
        label={t("admin.eventSettings.timeZone")}
        hint={t("admin.eventSettings.timeZoneHint")}
        value={timeZone}
        onChange={setTimeZone}
        error={errs["time_zone"]}
      />
      <TextField
        label={t("setup.workspace.color")}
        hint={t("setup.workspace.colorHint")}
        value={primaryColor}
        onChange={setPrimaryColor}
        error={errs["primary_color"]}
      />
      <h2>{t("setup.workspace.owner")}</h2>
      <TextField
        label={t("setup.workspace.ownerEmail")}
        type="email"
        value={ownerEmail}
        onChange={setOwnerEmail}
        error={errs["ownerEmail"]}
        autoComplete="email"
        required
      />
      <TextField
        label={t("setup.workspace.ownerName")}
        value={ownerName}
        onChange={setOwnerName}
        error={errs["ownerName"]}
        autoComplete="name"
      />
    </StepForm>
  );
}
