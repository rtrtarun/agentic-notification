// Package mail sends SMTP messages with context cancellation, which the
// standard library's smtp.SendMail does not support.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
)

// ErrSTARTTLSRequired is returned when strict mode is on and the server does not offer
// STARTTLS. Nothing, including credentials and the message, has been sent by then.
var ErrSTARTTLSRequired = errors.New("the smtp server does not offer STARTTLS and plaintext mail is not allowed")

// Options tune a send. The zero value is the historical behaviour.
type Options struct {
	// RequireStartTLS refuses to send unless the connection is upgraded to TLS first.
	// Without it, STARTTLS is used when offered and mail goes in plaintext when it is not.
	RequireStartTLS bool
}

// Send delivers msg through the SMTP server at addr ("host:port").
// Cancelling ctx aborts the dial, the SMTP conversation and any blocked I/O.
// STARTTLS is used when the server offers it; auth may be nil.
func Send(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return SendWith(ctx, Options{}, addr, auth, from, to, msg)
}

// SendWith is Send with options. With RequireStartTLS, a server that does not offer
// STARTTLS (or whose upgrade fails) is an error before credentials or mail are sent.
func SendWith(ctx context.Context, opts Options, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse smtp address %q: %w", addr, err)
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp server: %w", err)
	}
	// Closing the connection unblocks any read or write in flight.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return ctxErrOr(ctx, fmt.Errorf("smtp handshake: %w", err))
	}
	defer client.Close()

	starttls, _ := client.Extension("STARTTLS")
	if opts.RequireStartTLS && !starttls {
		return ErrSTARTTLSRequired
	}
	if starttls {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return ctxErrOr(ctx, fmt.Errorf("smtp starttls: %w", err))
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return ctxErrOr(ctx, fmt.Errorf("smtp auth: %w", err))
			}
		}
	}
	if err := client.Mail(from); err != nil {
		return ctxErrOr(ctx, fmt.Errorf("smtp mail from: %w", err))
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return ctxErrOr(ctx, fmt.Errorf("smtp rcpt: %w", err))
		}
	}
	w, err := client.Data()
	if err != nil {
		return ctxErrOr(ctx, fmt.Errorf("smtp data: %w", err))
	}
	if _, err := w.Write(msg); err != nil {
		return ctxErrOr(ctx, fmt.Errorf("smtp write: %w", err))
	}
	if err := w.Close(); err != nil {
		return ctxErrOr(ctx, fmt.Errorf("smtp finish data: %w", err))
	}
	return ctxErrOr(ctx, client.Quit())
}

// ctxErrOr prefers the context's error: when ctx is done the connection was
// closed under us, so the I/O error is only a symptom.
func ctxErrOr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("smtp send aborted: %w", ctxErr)
	}
	return err
}
