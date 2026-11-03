# Send email with Gmail

EventPass sends passes and sign-in links over SMTP. A free Gmail account works for events of up to a few hundred people. Any SMTP provider works the same way (see the end of this page).

## 1. Use a dedicated account

Create a Gmail account for your club or event, for example `devclub.events@gmail.com`. Participants see this address, and you keep event mail out of a personal inbox.

## 2. Turn on 2-Step Verification

Google only issues App Passwords to accounts with 2-Step Verification.

1. Open <https://myaccount.google.com/security> while signed in as that account.
2. Under **How you sign in to Google**, turn on **2-Step Verification** and follow the steps.

## 3. Create an App Password

1. Open <https://myaccount.google.com/apppasswords>.
2. Enter a name such as `EventPass` and click **Create**.
3. Copy the 16-character password. Google shows it only once.

The App Password is not your Google password. You can revoke it on the same page at any time.

> No App Passwords page? The account may be a Google Workspace account whose admin turned them off, or 2-Step Verification may not be on yet.

## 4. Give it to EventPass

In the setup wizard, choose **Gmail** and fill in:

| Field | Value |
| --- | --- |
| SMTP server | `smtp.gmail.com` |
| Port | `587` |
| Username | the full Gmail address |
| Password | the App Password, without spaces |
| From | `Dev Club <devclub.events@gmail.com>` |

The wizard sends a test email before it continues. Check that it arrived, and look in spam too.

Or use environment variables, which override the wizard:

```sh
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=devclub.events@gmail.com
SMTP_PASSWORD=abcdefghijklmnop
SMTP_FROM="Dev Club <devclub.events@gmail.com>"
```

Send a test from the command line at any time:

```sh
eventpass check --send-test you@example.org
```

## Sending limits

Gmail allows about 500 messages a day from a free account. EventPass:

- stops at **450 per rolling 24 hours** and leaves the rest queued, so sign-in links still go out;
- sends one message every 1.5 seconds, so a burst of registrations doesn't trip Gmail's spam checks;
- keeps every message in the event's **Outbox** tab, so a restart loses nothing; failures are retried after 2, 4, 8 and 16 minutes.

For bigger events, use a provider with a higher free tier. Brevo, Mailgun and Amazon SES all offer SMTP; pick them in the wizard and use the SMTP credentials from their dashboards.

## Encryption

EventPass always encrypts the connection: STARTTLS on port 587, implicit TLS on port 465. It refuses a server that offers neither rather than send passwords in plain text.

## If mail doesn't arrive

- **"Username and Password not accepted"**: you used the normal Google password, or the App Password was revoked. Create a new App Password.
- **Mail lands in spam**: send from a dedicated address, keep the From name stable and ask participants to add the address to their contacts. For a custom domain, set up SPF and DKIM with your provider.
- **Email shows "Daily cap reached"** on the control panel's System page: queued mail waits for the 24-hour window to roll over. Switch providers for bigger events.
