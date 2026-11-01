# mail

Context-aware SMTP sending. `smtp.SendMail` in the standard library cannot be cancelled; `mail.Send` dials with the context, closes the connection when the context ends, and returns the context error so callers can use `errors.Is(err, context.Canceled)`.

Used by `pkg/delivery` (SMTP provider) and `pkg/notification/impl`.
