// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server: EHLO, STARTTLS, AUTH PLAIN/LOGIN,
// MAIL, RCPT, DATA, RSET, NOOP, QUIT. It records delivered messages.
type fakeSMTP struct {
	addr      string
	tlsConf   *tls.Config
	noTLS     bool   // don't offer STARTTLS
	password  string // AUTH must use this password
	tempFail  bool   // answer 451 to DATA
	mu        sync.Mutex
	delivered []string
	ln        net.Listener
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startFake(t *testing.T, f *fakeSMTP) *fakeSMTP {
	t.Helper()
	f.tlsConf = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}, MinVersion: tls.VersionTLS12}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln, f.addr = ln, ln.Addr().String()
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
	secure := false
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO") || strings.HasPrefix(up, "HELO"):
			_, _ = w.WriteString("250-fake\r\n")
			if !f.noTLS && !secure {
				_, _ = w.WriteString("250-STARTTLS\r\n")
			}
			say("250 AUTH PLAIN LOGIN")
		case up == "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, f.tlsConf)
			if tc.Handshake() != nil {
				return
			}
			c, secure = tc, true
			r, w = bufio.NewReader(tc), bufio.NewWriter(tc)
			say = func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
		case strings.HasPrefix(up, "AUTH PLAIN"):
			parts := strings.Fields(cmd)
			var b []byte
			if len(parts) == 3 {
				b, _ = base64.StdEncoding.DecodeString(parts[2])
			}
			fields := strings.Split(string(b), "\x00")
			if len(fields) == 3 && fields[2] == f.password {
				say("235 ok")
			} else {
				say("535 5.7.8 Username and Password not accepted")
			}
		case strings.HasPrefix(up, "AUTH LOGIN"):
			say("334 VXNlcm5hbWU6")
			_, _ = r.ReadString('\n')
			say("334 UGFzc3dvcmQ6")
			p, _ := r.ReadString('\n')
			pw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			if string(pw) == f.password {
				say("235 ok")
			} else {
				say("535 5.7.8 Username and Password not accepted")
			}
		case strings.HasPrefix(up, "MAIL FROM"), strings.HasPrefix(up, "RCPT TO"), up == "RSET", up == "NOOP":
			say("250 ok")
		case up == "DATA":
			if f.tempFail {
				say("451 4.3.0 try again later")
				continue
			}
			say("354 end with .")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(strings.TrimPrefix(l, "."))
			}
			f.mu.Lock()
			f.delivered = append(f.delivered, sb.String())
			f.mu.Unlock()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

func (f *fakeSMTP) config() SMTPConfig {
	host, port, _ := net.SplitHostPort(f.addr)
	p, _ := strconv.Atoi(port)
	return SMTPConfig{Host: host, Port: p, Username: "events@example.org", Password: "app-password",
		From: "EventPass <events@example.org>", TLSConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // test server
}

func sample() Message {
	return Message{
		To: "ada@example.org", Subject: "Your pass for DevFest",
		Text: "Hi Ada\nYour pass is attached.", HTML: `<p>Hi Ada</p><img src="cid:qr@eventpass">`,
		Attachments: []Attachment{
			{Filename: "qr.png", ContentType: "image/png", Inline: true, ContentID: "qr@eventpass", Data: []byte("\x89PNGqr")},
			{Filename: "pass.png", ContentType: "image/png", Data: []byte("\x89PNGcard")},
			{Filename: "pass.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.3")},
		},
	}
}

func TestSMTPSendsOverSTARTTLS(t *testing.T) {
	f := startFake(t, &fakeSMTP{password: "app-password"})
	if err := NewSMTP(f.config()).Send(context.Background(), sample()); err != nil {
		t.Fatal(err)
	}
	if len(f.delivered) != 1 {
		t.Fatalf("delivered %d messages", len(f.delivered))
	}
	parts := mimeParts(t, f.delivered[0])
	want := map[string]bool{"text/plain": false, "text/html": false, "inline:image/png:qr@eventpass": false,
		"attachment:image/png:pass.png": false, "attachment:application/pdf:pass.pdf": false}
	for _, p := range parts {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("missing part %s; got %v", k, parts)
		}
	}
}

// mimeParts walks a message and describes its leaf parts.
func mimeParts(t *testing.T, raw string) []string {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Subject") != "Your pass for DevFest" || !strings.Contains(msg.Header.Get("To"), "ada@example.org") {
		t.Fatalf("headers: %v", msg.Header)
	}
	var out []string
	var walk func(ct string, body io.Reader, disp, cid string)
	walk = func(ct string, body io.Reader, disp, cid string) {
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil {
			t.Fatalf("content type %q: %v", ct, err)
		}
		if strings.HasPrefix(mt, "multipart/") {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				walk(p.Header.Get("Content-Type"), p, p.Header.Get("Content-Disposition"), p.Header.Get("Content-ID"))
			}
		}
		d, dp, _ := mime.ParseMediaType(disp)
		switch d {
		case "inline":
			if mt != "text/plain" && mt != "text/html" {
				out = append(out, "inline:"+mt+":"+strings.Trim(cid, "<>"))
				return
			}
		case "attachment":
			out = append(out, "attachment:"+mt+":"+dp["filename"])
			return
		}
		out = append(out, mt)
	}
	walk(msg.Header.Get("Content-Type"), msg.Body, "", "")
	return out
}

func TestSMTPRequiresTLS(t *testing.T) {
	f := startFake(t, &fakeSMTP{password: "app-password", noTLS: true})
	err := NewSMTP(f.config()).Send(context.Background(), sample())
	if err == nil || len(f.delivered) != 0 {
		t.Fatalf("sent without encryption: %v", err)
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("error should explain the TLS problem: %v", err)
	}
}

func TestSMTPAuthFailureIsPermanentAndExplained(t *testing.T) {
	f := startFake(t, &fakeSMTP{password: "right"})
	err := NewSMTP(f.config()).Send(context.Background(), sample())
	if !errors.Is(err, ErrPermanent) || !strings.Contains(err.Error(), "App Password") {
		t.Fatalf("auth failure: %v", err)
	}
}

func TestSMTPTemporaryFailureIsRetryable(t *testing.T) {
	f := startFake(t, &fakeSMTP{password: "app-password", tempFail: true})
	err := NewSMTP(f.config()).Send(context.Background(), sample())
	if err == nil || errors.Is(err, ErrPermanent) {
		t.Fatalf("451 should be a retryable error: %v", err)
	}
}

func TestSMTPUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()
	err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: addr.Port, From: "a@x.org"}).Send(context.Background(), sample())
	if err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestSMTPConfigValidate(t *testing.T) {
	if err := (SMTPConfig{}).Validate(); err == nil || !strings.Contains(err.Error(), "SMTP_HOST") {
		t.Fatalf("Validate: %v", err)
	}
}
