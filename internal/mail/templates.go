// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

// EmailData is what the built-in templates can use. Editable templates with
// {{participant.name}}-style variables replace these in M4.
type EmailData struct {
	Product   string
	EventName string
	When      string
	Venue     string
	Address   string
	FullName  string
	PassID    string
	PortalURL string
	Primary   string
	Support   string
	Footer    string
	QRCID     string // set when a QR image is attached inline
}

type template struct {
	subject string
	text    string
	html    string // body inside the branded layout
}

var templates = map[string]template{
	domain.TemplatePassIssued: {
		subject: "Your pass for {{.EventName}}",
		text: `Hi {{.FullName}},

You're registered for {{.EventName}}.
{{if .When}}When: {{.When}}
{{end}}{{if .Venue}}Where: {{.Venue}}{{if .Address}}, {{.Address}}{{end}}
{{end}}
Your pass ID is {{.PassID}}. Show the QR code (attached as pass.png and pass.pdf) at the entrance.

Open your pass, schedule and map at any time:
{{.PortalURL}}
`,
		html: `<p>Hi {{.FullName}},</p>
<p>You're registered for <strong>{{.EventName}}</strong>.</p>
{{if .When}}<p><strong>When:</strong> {{.When}}</p>{{end}}
{{if .Venue}}<p><strong>Where:</strong> {{.Venue}}{{if .Address}}, {{.Address}}{{end}}</p>{{end}}
<p>Show this QR code at the entrance:</p>
{{if .QRCID}}<p style="text-align:center"><img src="cid:{{.QRCID}}" width="240" height="240" alt="QR code for pass {{.PassID}}" style="display:inline-block;border:0"></p>{{end}}
<p style="text-align:center;font-family:monospace;font-size:20px;letter-spacing:1px"><strong>{{.PassID}}</strong></p>
<p>The pass is also attached as an image and a printable PDF.</p>`,
	},
	domain.TemplateRegistrationReceived: {
		subject: "We received your registration for {{.EventName}}",
		text: `Hi {{.FullName}},

Thanks for registering for {{.EventName}}. The organizers review each registration; we'll email your pass as soon as yours is approved.

You can check your status at any time:
{{.PortalURL}}
`,
		html: `<p>Hi {{.FullName}},</p>
<p>Thanks for registering for <strong>{{.EventName}}</strong>. The organizers review each registration; we'll email your pass as soon as yours is approved.</p>`,
	},
	domain.TemplateWaitlisted: {
		subject: "You're on the waitlist for {{.EventName}}",
		text: `Hi {{.FullName}},

{{.EventName}} is full right now, so you're on the waitlist. If a spot opens up we'll email your pass.

You can check your status at any time:
{{.PortalURL}}
`,
		html: `<p>Hi {{.FullName}},</p>
<p><strong>{{.EventName}}</strong> is full right now, so you're on the waitlist. If a spot opens up we'll email your pass.</p>`,
	},
}

// layout wraps every HTML body in the workspace branding. Tables and inline
// styles because email clients ignore stylesheets.
const layout = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f6f7f9;font-family:Arial,Helvetica,sans-serif;color:#1a1d23">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f6f7f9;padding:24px 12px"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;background:#ffffff;border-radius:12px;overflow:hidden">
<tr><td style="background:{{.Data.Primary}};color:{{.Ink}};padding:20px 24px">
<div style="font-size:13px;opacity:.9">{{.Data.Product}}</div>
<div style="font-size:20px;font-weight:bold;margin-top:4px">{{.Data.EventName}}</div>
</td></tr>
<tr><td style="padding:20px 24px;font-size:15px;line-height:1.55">{{.Body}}
<p style="margin-top:24px"><a href="{{.Data.PortalURL}}" style="display:inline-block;padding:12px 20px;background:{{.Data.Primary}};color:{{.Ink}};text-decoration:none;border-radius:6px;font-weight:bold">Open my pass</a></p>
</td></tr>
<tr><td style="padding:16px 24px;border-top:1px solid #d6dae1;font-size:12px;color:#4d5462">
{{if .Data.Support}}Questions? Write to <a href="mailto:{{.Data.Support}}" style="color:#4d5462">{{.Data.Support}}</a>.<br>{{end}}
{{.Data.Footer}}
</td></tr>
</table></td></tr></table></body></html>`

var layoutTmpl = htmltemplate.Must(htmltemplate.New("layout").Parse(layout))

// Render produces subject, text and HTML for a template key.
func Render(key string, d EmailData) (subject, text, html string, err error) {
	t, ok := templates[key]
	if !ok {
		return "", "", "", fmt.Errorf("unknown email template %q", key)
	}
	if d.Primary == "" || !domain.ValidColor(d.Primary) {
		d.Primary = "#1B4DDB"
	}
	run := func(src string) (string, error) {
		tt, err := texttemplate.New(key).Parse(src)
		if err != nil {
			return "", err
		}
		var b bytes.Buffer
		err = tt.Execute(&b, d)
		return b.String(), err
	}
	if subject, err = run(t.subject); err != nil {
		return
	}
	subject = strings.TrimSpace(subject)
	if text, err = run(t.text); err != nil {
		return
	}
	if d.Footer != "" {
		text += "\n--\n" + d.Footer + "\n"
	}
	// HTML bodies go through html/template so participant input is escaped.
	bt, err := htmltemplate.New(key).Parse(t.html)
	if err != nil {
		return
	}
	var body bytes.Buffer
	if err = bt.Execute(&body, d); err != nil {
		return
	}
	ink := "#ffffff"
	if domain.ContrastRatio(d.Primary, "#FFFFFF") < domain.ContrastRatio(d.Primary, "#000000") {
		ink = "#000000"
	}
	var out bytes.Buffer
	err = layoutTmpl.Execute(&out, map[string]any{
		"Data": d, "Ink": ink, "Body": htmltemplate.HTML(body.String()), //nolint:gosec // produced by html/template above
	})
	return subject, text, out.String(), err
}
