// Package notify tells the duck's owner when a new registration needs vetting.
package notify

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Notifier delivers a short message to the owner.
type Notifier interface {
	Notify(ctx context.Context, subject, body string) error
}

// Multi sends to every notifier, logging (not failing on) individual errors.
type Multi []Notifier

func (m Multi) Notify(ctx context.Context, subject, body string) error {
	for _, n := range m {
		if err := n.Notify(ctx, subject, body); err != nil {
			log.Printf("notify %T: %v", n, err)
		}
	}
	return nil
}

// Log writes notifications to the server log (handy for local testing).
type Log struct{}

func (Log) Notify(_ context.Context, subject, body string) error {
	log.Printf("NOTIFY: %s\n%s", subject, body)
	return nil
}

// Email sends plain-text email over SMTP with STARTTLS (e.g. port 587).
type Email struct {
	Host, Port, Username, Password, From, To string
}

func (e Email) Notify(ctx context.Context, subject, body string) error {
	return e.SendTo(ctx, e.To, subject, body)
}

// SendTo sends to a specific recipient instead of e.To.
func (e Email) SendTo(_ context.Context, to, subject, body string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return fmt.Errorf("invalid header value")
	}
	msg := strings.Join([]string{
		"From: " + e.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	var auth smtp.Auth
	if e.Username != "" {
		auth = smtp.PlainAuth("", e.Username, e.Password, e.Host)
	}
	var rcpt []string
	for addr := range strings.SplitSeq(to, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			rcpt = append(rcpt, addr)
		}
	}
	return smtp.SendMail(e.Host+":"+e.Port, auth, e.From, rcpt, []byte(msg))
}

// SMS sends a text message through Twilio's REST API.
type SMS struct {
	AccountSID, AuthToken, From, To string
	Client                          *http.Client
}

func (s SMS) Notify(ctx context.Context, subject, body string) error {
	form := url.Values{"To": {s.To}, "From": {s.From}, "Body": {subject + "\n" + body}}
	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", s.AccountSID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.AccountSID, s.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("twilio: %s", resp.Status)
	}
	return nil
}
