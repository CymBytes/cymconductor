// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"fmt"
	"math/rand"
	"net/smtp"
	"time"

	"github.com/rs/zerolog"
)

// EmailTrafficHandler handles simulate_email_traffic actions.
type EmailTrafficHandler struct {
	config EmailTrafficConfig
	logger zerolog.Logger
}

// NewEmailTrafficHandler creates a new email traffic handler.
func NewEmailTrafficHandler(cfg EmailTrafficConfig, logger zerolog.Logger) *EmailTrafficHandler {
	return &EmailTrafficHandler{
		config: cfg,
		logger: logger.With().Str("action", "email_traffic").Logger(),
	}
}

// Execute simulates email activity.
func (h *EmailTrafficHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting email traffic simulation")

	// Parse parameters
	protocol := "smtp"
	if p, ok := getString(params, "protocol"); ok && p != "" {
		protocol = p
	}
	_ = protocol // Protocol selection reserved for future use (IMAP/POP3)

	server, _ := getString(params, "server")
	if server == "" {
		return nil, fmt.Errorf("server parameter is required")
	}

	port, _ := getInt(params, "port")
	if port == 0 {
		port = 25
	}

	username, _ := getString(params, "username")
	password, _ := getString(params, "password")

	actions, _ := getStringSlice(params, "actions")
	if len(actions) == 0 {
		actions = []string{"send"}
	}

	emailCount, _ := getInt(params, "email_count")
	if emailCount == 0 {
		emailCount = 3
	}

	recipients, _ := getStringSlice(params, "recipients")
	subjectTemplate, _ := getString(params, "subject_template")
	if subjectTemplate == "" {
		subjectTemplate = "Test Email"
	}

	bodyTemplate, _ := getString(params, "body_template")
	if bodyTemplate == "" {
		bodyTemplate = "This is a test email from CymConductor."
	}

	// Track operations
	emailsSent := 0
	emailsReceived := 0
	emailsFailed := 0

	// Process actions
	for _, action := range actions {
		switch action {
		case "send":
		sendLoop:
			for i := 0; i < emailCount; i++ {
				select {
				case <-ctx.Done():
					break sendLoop
				default:
				}

				if len(recipients) == 0 {
					h.logger.Warn().Msg("No recipients specified for send action")
					continue
				}

				recipient := recipients[rand.Intn(len(recipients))]
				subject := fmt.Sprintf("%s - %d", subjectTemplate, time.Now().UnixNano())
				body := fmt.Sprintf("%s\n\nSent at: %s", bodyTemplate, time.Now().Format(time.RFC3339))

				err := h.sendEmail(server, port, username, password, recipient, subject, body)
				if err != nil {
					h.logger.Warn().Err(err).Str("recipient", recipient).Msg("Failed to send email")
					emailsFailed++
				} else {
					emailsSent++
					h.logger.Debug().Str("recipient", recipient).Msg("Email sent")
				}

				// Random delay between sends
				time.Sleep(time.Duration(500+rand.Intn(2000)) * time.Millisecond)
			}

		case "receive", "list", "read":
			// IMAP operations would be implemented here
			// For now, we simulate the activity
			h.logger.Info().Str("action", action).Msg("Simulating IMAP operation (stub)")
			time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)
			emailsReceived += rand.Intn(5)
		}
	}

	duration := time.Since(startTime)
	h.logger.Info().
		Int("sent", emailsSent).
		Int("received", emailsReceived).
		Int("failed", emailsFailed).
		Dur("duration", duration).
		Msg("Email traffic simulation complete")

	return &Result{
		Data: map[string]interface{}{
			"emails_sent":     emailsSent,
			"emails_received": emailsReceived,
			"emails_failed":   emailsFailed,
		},
		Summary:    fmt.Sprintf("Sent %d emails, received %d, %d failed", emailsSent, emailsReceived, emailsFailed),
		DurationMs: duration.Milliseconds(),
	}, nil
}

func (h *EmailTrafficHandler) sendEmail(server string, port int, username, password, recipient, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", server, port)
	from := username
	if from == "" {
		from = "agent@cymbytes.local"
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		from, recipient, subject, body)

	var auth smtp.Auth
	if username != "" && password != "" {
		auth = smtp.PlainAuth("", username, password, server)
	}

	err := smtp.SendMail(addr, auth, from, []string{recipient}, []byte(msg))
	if err != nil {
		return fmt.Errorf("SMTP send failed: %w", err)
	}

	return nil
}
