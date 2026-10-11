package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Sender interface {
	Send(context.Context, Config, string, string, string) error
}
type SMTPSender struct{}

// TCPProbe checks the configured SMTP gateway with a short connection attempt.
// No credentials or email are sent by the probe.
func TCPProbe(ctx context.Context, c Config) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, fmt.Sprint(c.Port)))
	if err != nil {
		return err
	}
	return conn.Close()
}
func (SMTPSender) Send(ctx context.Context, c Config, subject, body, messageID string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	d := net.Dialer{Timeout: 5 * time.Second}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	var conn net.Conn
	var err error
	if c.Security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("SMTP connection: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer client.Close()
	if c.Security == "starttls" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err = client.StartTLS(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if err = client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
		return fmt.Errorf("SMTP authentication: %w", err)
	}
	if err = client.Mail(c.From); err != nil {
		return fmt.Errorf("SMTP sender: %w", err)
	}
	if err = client.Rcpt(c.To); err != nil {
		return fmt.Errorf("SMTP recipient: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	subject = mime.QEncoding.Encode("utf-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(subject))
	var data bytes.Buffer
	fmt.Fprintf(&data, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s@apc-ups-bridge.local>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n", c.From, c.To, subject, messageID)
	// Use correct SMTP CRLF line endings, including on multiline reports.
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	_, err = io.WriteString(&data, strings.ReplaceAll(body, "\n", "\r\n")+"\r\n")
	if err != nil {
		return err
	}
	if _, err = w.Write(data.Bytes()); err != nil {
		return fmt.Errorf("SMTP message write: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("SMTP delivery: %w", err)
	}
	// The server has accepted the message. A failure while closing the session
	// must not make a delivered message look failed (it would be resent).
	_ = client.Quit()
	return nil
}
