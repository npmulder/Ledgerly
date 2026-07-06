// Package mail provides the minimal outbound email abstraction used by modules.
package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime/multipart"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	envHost = "LEDGERLY_SMTP_HOST"
	envPort = "LEDGERLY_SMTP_PORT"
	envUser = "LEDGERLY_SMTP_USER"
	envPass = "LEDGERLY_SMTP_PASS"
	envFrom = "LEDGERLY_SMTP_FROM"
)

// Sender sends one outbound message.
type Sender interface {
	Send(context.Context, Message) error
}

// Message is a plain-text email with optional attachments.
type Message struct {
	To          string
	Subject     string
	TextBody    string
	Attachments []Attachment
}

// Attachment is an in-memory mail attachment.
type Attachment struct {
	Filename    string
	ContentType string
	Bytes       []byte
}

// SMTPConfig contains the environment-backed SMTP settings.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// SMTPSender sends messages with net/smtp.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender creates an SMTP-backed sender.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	return &SMTPSender{cfg: cfg}
}

// NewSMTPSenderFromEnv returns a sender configured from LEDGERLY_SMTP_*.
func NewSMTPSenderFromEnv() *SMTPSender {
	port, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(envPort)))
	return NewSMTPSender(SMTPConfig{
		Host:     strings.TrimSpace(os.Getenv(envHost)),
		Port:     port,
		Username: strings.TrimSpace(os.Getenv(envUser)),
		Password: os.Getenv(envPass),
		From:     strings.TrimSpace(os.Getenv(envFrom)),
	})
}

// Send validates and sends msg.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := s.cfg
	if err := validateSMTPConfig(cfg); err != nil {
		return err
	}
	msg = normalizeMessage(msg)
	if err := validateMessage(msg); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	var auth smtp.Auth
	if cfg.Username != "" || cfg.Password != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	data, err := buildMessageBytes(cfg.From, msg)
	if err != nil {
		return err
	}
	return smtp.SendMail(addr, auth, cfg.From, []string{msg.To}, data)
}

func validateSMTPConfig(cfg SMTPConfig) error {
	var missing []string
	if cfg.Host == "" {
		missing = append(missing, envHost)
	}
	if cfg.Port <= 0 {
		missing = append(missing, envPort)
	}
	if cfg.From == "" {
		missing = append(missing, envFrom)
	}
	if len(missing) > 0 {
		return fmt.Errorf("mail: missing SMTP config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func normalizeMessage(msg Message) Message {
	msg.To = strings.TrimSpace(msg.To)
	msg.Subject = strings.TrimSpace(msg.Subject)
	msg.TextBody = strings.TrimRight(strings.ReplaceAll(msg.TextBody, "\r\n", "\n"), "\n") + "\n"
	for i := range msg.Attachments {
		msg.Attachments[i].Filename = strings.TrimSpace(msg.Attachments[i].Filename)
		msg.Attachments[i].ContentType = strings.TrimSpace(msg.Attachments[i].ContentType)
	}
	return msg
}

func validateMessage(msg Message) error {
	if msg.To == "" {
		return errors.New("mail: message to is required")
	}
	if msg.Subject == "" {
		return errors.New("mail: message subject is required")
	}
	if strings.TrimSpace(msg.TextBody) == "" {
		return errors.New("mail: message text body is required")
	}
	for _, attachment := range msg.Attachments {
		if attachment.Filename == "" {
			return errors.New("mail: attachment filename is required")
		}
		if attachment.ContentType == "" {
			return errors.New("mail: attachment content type is required")
		}
		if len(attachment.Bytes) == 0 {
			return errors.New("mail: attachment bytes are required")
		}
	}
	return nil
}

func buildMessageBytes(from string, msg Message) ([]byte, error) {
	var buf bytes.Buffer
	writeHeader(&buf, "From", from)
	writeHeader(&buf, "To", msg.To)
	writeHeader(&buf, "Subject", msg.Subject)
	writeHeader(&buf, "MIME-Version", "1.0")

	if len(msg.Attachments) == 0 {
		writeHeader(&buf, "Content-Type", `text/plain; charset="utf-8"`)
		buf.WriteString("\r\n")
		buf.WriteString(strings.ReplaceAll(msg.TextBody, "\n", "\r\n"))
		return buf.Bytes(), nil
	}

	writer := multipart.NewWriter(&buf)
	writeHeader(&buf, "Content-Type", `multipart/mixed; boundary="`+writer.Boundary()+`"`)
	buf.WriteString("\r\n")

	textPart, err := writer.CreatePart(map[string][]string{
		"Content-Type": {"text/plain; charset=\"utf-8\""},
	})
	if err != nil {
		return nil, fmt.Errorf("mail: create text part: %w", err)
	}
	if _, err := textPart.Write([]byte(msg.TextBody)); err != nil {
		return nil, fmt.Errorf("mail: write text part: %w", err)
	}

	for _, attachment := range msg.Attachments {
		part, err := writer.CreatePart(map[string][]string{
			"Content-Type":              {attachment.ContentType},
			"Content-Disposition":       {`attachment; filename="` + attachment.Filename + `"`},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, fmt.Errorf("mail: create attachment part: %w", err)
		}
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(attachment.Bytes)))
		base64.StdEncoding.Encode(encoded, attachment.Bytes)
		for len(encoded) > 76 {
			if _, err := part.Write(append(encoded[:76], '\r', '\n')); err != nil {
				return nil, fmt.Errorf("mail: write attachment part: %w", err)
			}
			encoded = encoded[76:]
		}
		if len(encoded) > 0 {
			if _, err := part.Write(append(encoded, '\r', '\n')); err != nil {
				return nil, fmt.Errorf("mail: write attachment part: %w", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("mail: close message: %w", err)
	}
	return buf.Bytes(), nil
}

func writeHeader(buf *bytes.Buffer, key string, value string) {
	safeValue := strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(safeValue)
	buf.WriteString("\r\n")
}

// MemorySender captures sent messages for harness and unit tests.
type MemorySender struct {
	mu       sync.Mutex
	messages []Message
}

// NewMemorySender returns an in-memory fake sender.
func NewMemorySender() *MemorySender {
	return &MemorySender{}
}

// Send stores a defensive copy of msg.
func (s *MemorySender) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, cloneMessage(msg))
	return nil
}

// Messages returns defensive copies of captured messages.
func (s *MemorySender) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Message, len(s.messages))
	for i, msg := range s.messages {
		result[i] = cloneMessage(msg)
	}
	return result
}

func cloneMessage(msg Message) Message {
	msg.Attachments = append([]Attachment{}, msg.Attachments...)
	for i := range msg.Attachments {
		msg.Attachments[i].Bytes = append([]byte{}, msg.Attachments[i].Bytes...)
	}
	return msg
}
