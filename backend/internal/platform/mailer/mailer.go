package mailer

import (
	"backend/internal/platform/config"
	"backend/internal/platform/telemetry"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// Mailer is the abstract email / SMS / push channel. Concrete drivers
// (SMTP, SendGrid, SES, bKash SMS) implement this later without touching
// any caller.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
	Provider() string
	Close(ctx context.Context) error
}

// Message is the common payload every driver understands. Body may be text
// or HTML, or both may be empty when TemplateID is set (provider-side
// template).
type Message struct {
	To           []string
	CC           []string
	BCC          []string
	Subject      string
	Text         string
	HTML         string
	Attachments  []Attachment
	Headers      map[string]string
	TemplateID   string
	TemplateVars map[string]any
}

// Attachment is a file to send.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Validate checks that msg is well-formed enough to attempt delivery.
// Every driver's Send must call this first — see instrumentedSend, which
// does it centrally so no driver can forget.
func (m Message) Validate() error {
	if len(m.To) == 0 && len(m.CC) == 0 && len(m.BCC) == 0 {
		return ErrNoRecipients
	}
	// A template-driven message may leave Subject/Text/HTML empty because
	// the provider's template supplies them.
	if m.TemplateID != "" {
		return nil
	}

	if strings.TrimSpace(m.Subject) == "" {
		return ErrEmptySubject
	}
	if m.Text == "" && m.HTML == "" {
		return ErrEmptyBody
	}

	return nil
}

// Sentinel errors. Callers can match these with errors.Is instead of
// string-matching driver error messages.
var (
	ErrNotConfigured = errors.New("mailer: not configured")
	ErrNoRecipients  = errors.New("mailer: message has no recipients")
	ErrEmptySubject  = errors.New("mailer: message has no subject or template")
	ErrEmptyBody     = errors.New("mailer: message has no text/html body or template")
)

// New builds a Mailer from config. Unknown drivers fail fast at boot instead
// of silently falling back to Noop — a typo in MAILER_DRIVER should crash
// startup, not quietly disable every outbound email.
func New(cfg config.MailerConfig, log *zap.Logger, m *telemetry.Metrics) (Mailer, error) {
	if log == nil {
		log = zap.NewNop()
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "noop":
		return NewNoop(log, m), nil
	case "disabled":
		return NewDisabled(log, m), nil
	default:
		return nil, fmt.Errorf("mailer: unknown driver %q (want: noop|disabled)", cfg.Driver)
	}
}

func instrumentedSend(ctx context.Context, provider string, m *telemetry.Metrics, msg Message, fn func(ctx context.Context) error) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	attrs := []attribute.KeyValue{
		telemetry.AttrMailerProvider.String(provider),
		attribute.Int("mailer.recipients", len(msg.To)+len(msg.CC)+len(msg.BCC)),
	}
	if msg.TemplateID != "" {
		attrs = append(attrs, attribute.String("mailer.template", msg.TemplateID))
	}

	var onDone func(dur time.Duration, err error)
	if m != nil {
		onDone = func(dur time.Duration, err error) {
			outcome := "success"
			if err != nil {
				outcome = "error"
			}
			m.MailSendTotal.WithLabelValues(provider, outcome).Inc()
			m.MailSendDuration.WithLabelValues(provider).Observe(dur.Seconds())
		}
	}

	return telemetry.TracedOperation(ctx, "mailer."+provider+".send", attrs, fn, onDone)
}

type Noop struct {
	log *zap.Logger
	m   *telemetry.Metrics
}

// NewNoop constructs a Noop mailer. log may be nil (defaults to a no-op
// logger) so a stray zero-value construction elsewhere can't panic.
func NewNoop(log *zap.Logger, m *telemetry.Metrics) *Noop {
	if log == nil {
		log = zap.NewNop()
	}
	return &Noop{log: log, m: m}
}

func (n *Noop) Provider() string { return "noop" }

func (n *Noop) Send(ctx context.Context, msg Message) error {
	return instrumentedSend(ctx, n.Provider(), n.m, msg, func(ctx context.Context) error {
		telemetry.LoggerFromContext(ctx, n.safeLog()).Info("mailer noop: message not actually sent",
			zap.Strings("to", msg.To),
			zap.String("subject", msg.Subject),
			zap.String("template", msg.TemplateID),
			zap.Int("attachments", len(msg.Attachments)),
		)
		return nil
	})
}

func (n *Noop) Close(_ context.Context) error {
	return nil
}

func (n *Noop) safeLog() *zap.Logger {
	if n.log == nil {
		return zap.NewNop()
	}
	return n.log
}

// Disabled — always fails with ErrNotConfigured. Use where "pretend it
// worked" is unacceptable (e.g. a staging environment that must prove real
// email wiring before go-live) — the caller is forced to handle the failure
// instead of assuming delivery.

type Disabled struct {
	log *zap.Logger
	m   *telemetry.Metrics
}

func NewDisabled(log *zap.Logger, m *telemetry.Metrics) *Disabled {
	if log == nil {
		log = zap.NewNop()
	}
	return &Disabled{log: log, m: m}
}

func (d *Disabled) Provider() string {
	return "disabled"
}

func (d *Disabled) Send(ctx context.Context, msg Message) error {
	return instrumentedSend(ctx, d.Provider(), d.m, msg, func(ctx context.Context) error {
		telemetry.LoggerFromContext(ctx, d.safeLog()).Warn("mailer disabled: refusing to send",
			zap.Strings("to", msg.To),
			zap.String("subject", msg.Subject),
		)
		return ErrNotConfigured
	})
}

func (d *Disabled) Close(_ context.Context) error {
	return nil
}

func (d *Disabled) safeLog() *zap.Logger {
	if d.log == nil {
		return zap.NewNop()
	}
	return d.log
}

// Compile-time interface checks.
var (
	_ Mailer = (*Noop)(nil)
	_ Mailer = (*Disabled)(nil)
)
