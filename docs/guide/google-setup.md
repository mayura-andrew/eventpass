# Connect Google Sheets

EventPass keeps all its data in Google Sheets that you own. It reaches them through a **service account**: a Google account for software. This guide takes about ten minutes and costs nothing.

## 1. Create a Google Cloud project

1. Open <https://console.cloud.google.com/> and sign in with the Google account that should own the project (a club or organization account is best).
2. Click the project picker at the top, then **New project**. Name it, for example, `eventpass`, and click **Create**.

## 2. Enable the Google Sheets API

1. With the new project selected, open **APIs & Services → Library**.
2. Search for **Google Sheets API**, open it and click **Enable**.

EventPass doesn't need the Drive API: you create the spreadsheets yourself and share them.

## 3. Create the service account and its key

1. Open **IAM & Admin → Service Accounts** and click **Create service account**.
2. Name it `eventpass` and click **Create and continue**. Skip the optional role and user steps and click **Done**.
3. Open the new service account, go to the **Keys** tab and choose **Add key → Create new key → JSON**. Your browser downloads a `.json` file.

Keep this file safe: it lets anyone who has it edit every spreadsheet shared with the service account. Don't commit it to git or email it around.

The service account's email looks like `eventpass@your-project.iam.gserviceaccount.com`. You share spreadsheets with this address.

> If the key download is blocked, your organization may forbid service account keys (the `iam.disableServiceAccountKeyCreation` policy). Use a personal Google account for the project, or ask your Google Workspace admin for an exception.

## 4. Create the workspace spreadsheet

1. In Google Sheets, create a new, empty spreadsheet, for example **EventPass workspace**.
2. Click **Share**, paste the service account email, choose **Editor**, untick **Notify people** and click **Share**.
3. Copy the spreadsheet's link from the address bar.

EventPass creates the tabs (`Workspace`, `Users`, `Events`, `EventRoles`, `Templates`, `Audit`, `Meta`) itself. Don't rename or reorder its columns. Adding your own columns at the end, or your own tabs, is fine.

## 5. Give the key to EventPass

Choose one:

- **Setup wizard** (simplest): start EventPass, open `/admin/setup`, enter the setup token from the server log, then paste the JSON key and the spreadsheet link. The key is stored encrypted in `DATA_DIR/secrets.enc` under `EVENTPASS_SECRET_KEY`.
- **Environment variables**: put the file on the server and set `GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json` and `SYSTEM_SHEET_ID=<spreadsheet link or ID>`. Environment variables override anything saved in the wizard.

## 6. One spreadsheet per event

Each event has its own spreadsheet, so you can share one event's data with a colleague without exposing the others:

1. Create a new, empty spreadsheet and share it with the service account as **Editor**, as in step 4.
2. In the control panel, open the event, go to **Settings → Connect a spreadsheet** and paste the link.

EventPass refuses a spreadsheet that already belongs to another event or to the workspace.

## Check everything

```sh
eventpass check
```

It prints one line per check and how to fix any that fail.

## Limits worth knowing

- Google allows 60 Sheets API requests per minute per service account. EventPass stays under 50 by default (`SHEETS_QUOTA_PER_MIN`) and gives scanners priority over background work.
- A spreadsheet holds up to 10 million cells. 1,000 participants with 60 fields use about 60,000.
- Edits you make by hand in the sheet show up in EventPass within 15 seconds.
