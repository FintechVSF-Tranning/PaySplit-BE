package gmail

import (
	"context"
	"fmt"
	"html"
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

func (m *Mailer) SendVerification(ctx context.Context, to, name, otp string, expires time.Time) error {
	return m.sendOTP(ctx, to, "Xác thực email PaySplit của bạn", name, otp, expires, "Mã xác thực email của bạn là:")
}

func (m *Mailer) SendPasswordReset(ctx context.Context, to, name, otp string, expires time.Time) error {
	return m.sendOTP(ctx, to, "Đặt lại mật khẩu PaySplit của bạn", name, otp, expires, "Mã đặt lại mật khẩu của bạn là:")
}

func (m *Mailer) sendOTP(ctx context.Context, to, subject, name, otp string, expires time.Time, actionText string) error {
	msg := mail.NewMsg()
	if err := msg.FromFormat(m.fromName, m.username); err != nil {
		return err
	}
	if err := msg.To(to); err != nil {
		return err
	}
	msg.Subject(subject)
	remaining := time.Until(expires)
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	plain := fmt.Sprintf("Xin chào %s,\n\n%s %s\n\nMã OTP này có hiệu lực trong %d phút. Vui lòng không chia sẻ mã này với bất kỳ ai.\n", name, actionText, otp, minutes)
	body := fmt.Sprintf("<div style=\"font-family: Arial, sans-serif; line-height: 1.6; color: #333;\"><p>Xin chào <strong>%s</strong>,</p><p>%s</p><div style=\"font-size: 32px; font-weight: bold; letter-spacing: 6px; color: #2563eb; padding: 12px 0;\">%s</div><p>Mã OTP này có hiệu lực trong <strong>%s phút</strong>. Vui lòng không chia sẻ mã này với bất kỳ ai.</p></div>", html.EscapeString(name), html.EscapeString(actionText), html.EscapeString(otp), strconv.Itoa(minutes))
	msg.SetBodyString(mail.TypeTextPlain, plain)
	msg.AddAlternativeString(mail.TypeTextHTML, body)
	sendCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err := m.client.DialAndSendWithContext(sendCtx, msg); err != nil {
		return fmt.Errorf("send Gmail message: %w", err)
	}
	return nil
}
