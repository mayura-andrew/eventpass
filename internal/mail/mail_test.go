// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"strings"
	"testing"
)

func TestCleanStripsHeaderInjection(t *testing.T) {
	m, err := Message{To: "a@x.org", Subject: "Hi\r\nBcc: evil@x.org"}.Clean()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Fatalf("subject keeps CR/LF: %q", m.Subject)
	}
	for _, to := range []string{"a@x.org\r\nBcc: b@x.org", "Name <a@x.org>", "nope", ""} {
		if _, err := (Message{To: to}).Clean(); err == nil {
			t.Errorf("recipient %q accepted", to)
		}
	}
}

func TestCatcherAndQueue(t *testing.T) {
	c := NewCatcher()
	q := NewQueue(c, 10)
	q.Enqueue(Message{To: "a@x.org", Subject: "one", Text: "1"})
	q.Enqueue(Message{To: "b@x.org", Subject: "two", Text: "2"})
	q.Enqueue(Message{To: "bad", Subject: "dropped"})
	q.Close()
	got := c.List()
	if len(got) != 2 || got[0].Subject != "two" || got[1].Subject != "one" {
		t.Fatalf("List = %+v", got)
	}
	if err := c.Send(context.Background(), Message{To: "x"}); err == nil {
		t.Fatal("invalid recipient accepted")
	}
}
