package gmail

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"time"

	mail "github.com/wneessen/go-mail"

	"paysplit-backend/internal/config"
)

type Mailer struct {
	client                                  *mail.Client
	username, fromName, verifyURL, resetURL string
	timeout                                 time.Duration
}

func New(cfg config.SMTPConfig, verifyURL, resetURL string) (*Mailer, error) {
	client, err := mail.NewClient(cfg.Host, mail.WithPort(cfg.Port), mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(cfg.Username), mail.WithPassword(cfg.AppPassword), mail.WithTLSPolicy(mail.TLSMandatory))
	if err != nil {
		return nil, fmt.Errorf("create Gmail SMTP client: %w", err)
	}
	return &Mailer{client: client, username: cfg.Username, fromName: cfg.FromName, verifyURL: verifyURL, resetURL: resetURL, timeout: cfg.Timeout}, nil
}

func (m *Mailer) SendVerification(ctx context.Context, to, name, token string, expires time.Time) error {
	return m.send(ctx, to, "Verify your PaySplit email", name, token, expires, m.verifyURL, "Verify email")
}
func (m *Mailer) SendPasswordReset(ctx context.Context, to, name, token string, expires time.Time) error {
	return m.send(ctx, to, "Reset your PaySplit password", name, token, expires, m.resetURL, "Reset password")
}

func (m *Mailer) send(ctx context.Context, to, subject, name, token string, expires time.Time, base, label string) error {
	link, err := tokenURL(base, token)
	if err != nil {
		return err
	}
	msg := mail.NewMsg()
	if err = msg.FromFormat(m.fromName, m.username); err != nil {
		return err
	}
	if err = msg.To(to); err != nil {
		return err
	}
	msg.Subject(subject)
	remaining := time.Until(expires)
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	plain := fmt.Sprintf("Hello %s,\n\n%s: %s\n\nThis link expires in %d minutes.\n", name, label, link, minutes)
	body := fmt.Sprintf("<p>Hello %s,</p><p><a href=\"%s\">%s</a></p><p>This link expires in %s minutes.</p>", html.EscapeString(name), html.EscapeString(link), html.EscapeString(label), strconv.Itoa(minutes))
	msg.SetBodyString(mail.TypeTextPlain, plain)
	msg.AddAlternativeString(mail.TypeTextHTML, body)
	sendCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err = m.client.DialAndSendWithContext(sendCtx, msg); err != nil {
		return fmt.Errorf("send Gmail message: %w", err)
	}
	return nil
}

func tokenURL(base, token string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse auth callback URL: %w", err)
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
