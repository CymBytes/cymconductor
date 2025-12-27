// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"github.com/rs/zerolog"
)

// IMAPBackend implements EmailBackend for IMAP servers.
type IMAPBackend struct {
	logger zerolog.Logger
	client *client.Client
}

// NewIMAPBackend creates a new IMAP backend.
func NewIMAPBackend(logger zerolog.Logger) *IMAPBackend {
	return &IMAPBackend{
		logger: logger.With().Str("backend", "imap").Logger(),
	}
}

// Name returns the backend name.
func (b *IMAPBackend) Name() string {
	return "imap"
}

// Connect establishes connection to the IMAP server.
func (b *IMAPBackend) Connect(ctx context.Context, config *EmailBackendConfig) error {
	if config.Server == "" {
		return fmt.Errorf("IMAP server is required")
	}

	port := config.Port
	if port == 0 {
		if config.UseTLS {
			port = 993
		} else {
			port = 143
		}
	}

	addr := fmt.Sprintf("%s:%d", config.Server, port)

	var c *client.Client
	var err error

	if config.UseTLS {
		c, err = client.DialTLS(addr, &tls.Config{
			ServerName: config.Server,
		})
	} else {
		c, err = client.Dial(addr)
	}

	if err != nil {
		return fmt.Errorf("IMAP dial failed: %w", err)
	}

	if config.Username != "" {
		if err := c.Login(config.Username, config.Password); err != nil {
			c.Close()
			return fmt.Errorf("IMAP login failed: %w", err)
		}
	}

	b.client = c
	b.logger.Debug().Str("server", addr).Msg("IMAP connected")
	return nil
}

// Disconnect closes the IMAP connection.
func (b *IMAPBackend) Disconnect() error {
	if b.client != nil {
		b.client.Logout()
		b.client = nil
	}
	return nil
}

// ListEmails returns emails matching the filter.
func (b *IMAPBackend) ListEmails(ctx context.Context, filter *EmailFilter) ([]*EmailHeader, error) {
	if b.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	folder := filter.Folder
	if folder == "" {
		folder = "INBOX"
	}

	mbox, err := b.client.Select(folder, true)
	if err != nil {
		return nil, fmt.Errorf("select folder failed: %w", err)
	}

	if mbox.Messages == 0 {
		return []*EmailHeader{}, nil
	}

	// Build search criteria
	criteria := imap.NewSearchCriteria()
	if filter.Unread != nil && *filter.Unread {
		criteria.WithoutFlags = []string{imap.SeenFlag}
	}
	if !filter.Since.IsZero() {
		criteria.Since = filter.Since
	}
	if !filter.Before.IsZero() {
		criteria.Before = filter.Before
	}

	seqNums, err := b.client.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if len(seqNums) == 0 {
		return []*EmailHeader{}, nil
	}

	// Limit results
	maxResults := filter.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}
	if maxResults > len(seqNums) {
		maxResults = len(seqNums)
	}

	// Fetch latest emails (from the end of the list)
	seqset := new(imap.SeqSet)
	start := len(seqNums) - maxResults
	if start < 0 {
		start = 0
	}
	for i := len(seqNums) - 1; i >= start; i-- {
		seqset.AddNum(seqNums[i])
	}

	messages := make(chan *imap.Message, maxResults)
	done := make(chan error, 1)

	go func() {
		done <- b.client.Fetch(seqset, []imap.FetchItem{
			imap.FetchEnvelope,
			imap.FetchFlags,
			imap.FetchBodyStructure,
		}, messages)
	}()

	headers := []*EmailHeader{}
	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		header := &EmailHeader{
			MessageID:  fmt.Sprintf("%d", msg.SeqNum),
			Subject:    msg.Envelope.Subject,
			ReceivedAt: msg.Envelope.Date,
			IsRead:     hasFlag(msg.Flags, imap.SeenFlag),
		}

		if len(msg.Envelope.From) > 0 {
			header.Sender = msg.Envelope.From[0].Address()
		}

		// Check for attachments in body structure
		if msg.BodyStructure != nil {
			header.HasAttachment = hasAttachmentInStructure(msg.BodyStructure)
		}

		// Apply additional filters (subject, sender, has_attachment)
		if filter.Subject != "" && !strings.Contains(
			strings.ToLower(header.Subject),
			strings.ToLower(filter.Subject),
		) {
			continue
		}
		if filter.Sender != "" && !strings.Contains(
			strings.ToLower(header.Sender),
			strings.ToLower(filter.Sender),
		) {
			continue
		}
		if filter.HasAttachment != nil && *filter.HasAttachment != header.HasAttachment {
			continue
		}

		headers = append(headers, header)
	}

	if err := <-done; err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	return headers, nil
}

// ReadEmail retrieves full email content.
func (b *IMAPBackend) ReadEmail(ctx context.Context, messageID string) (*Email, error) {
	if b.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Parse sequence number from messageID
	var seqNum uint32
	if _, err := fmt.Sscanf(messageID, "%d", &seqNum); err != nil {
		return nil, fmt.Errorf("invalid message ID: %s", messageID)
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(seqNum)

	// Fetch full message
	messages := make(chan *imap.Message, 1)
	section := &imap.BodySectionName{}
	done := make(chan error, 1)

	go func() {
		done <- b.client.Fetch(seqset, []imap.FetchItem{
			imap.FetchEnvelope,
			imap.FetchFlags,
			imap.FetchBodyStructure,
			section.FetchItem(),
		}, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	email := &Email{
		EmailHeader: EmailHeader{
			MessageID:  messageID,
			IsRead:     hasFlag(msg.Flags, imap.SeenFlag),
		},
	}

	if msg.Envelope != nil {
		email.Subject = msg.Envelope.Subject
		email.ReceivedAt = msg.Envelope.Date
		if len(msg.Envelope.From) > 0 {
			email.Sender = msg.Envelope.From[0].Address()
		}
	}

	// Parse body for text and attachments
	r := msg.GetBody(section)
	if r != nil {
		mr, err := mail.CreateReader(r)
		if err != nil {
			b.logger.Warn().Err(err).Msg("Failed to create mail reader")
		} else {
			partIndex := 0
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					b.logger.Warn().Err(err).Msg("Error reading part")
					break
				}

				switch h := p.Header.(type) {
				case *mail.InlineHeader:
					contentType, _, _ := h.ContentType()
					body, _ := io.ReadAll(p.Body)
					if strings.HasPrefix(contentType, "text/plain") {
						email.Body = string(body)
					} else if strings.HasPrefix(contentType, "text/html") {
						email.BodyHTML = string(body)
					}
				case *mail.AttachmentHeader:
					filename, _ := h.Filename()
					contentType, _, _ := h.ContentType()

					// Get size by reading (we'll re-fetch for actual download)
					body, _ := io.ReadAll(p.Body)

					email.Attachments = append(email.Attachments, AttachmentInfo{
						AttachmentID: fmt.Sprintf("%d", partIndex),
						Filename:     filename,
						ContentType:  contentType,
						SizeBytes:    int64(len(body)),
					})
				}
				partIndex++
			}
		}
	}

	email.HasAttachment = len(email.Attachments) > 0
	return email, nil
}

// GetAttachment retrieves a specific attachment.
func (b *IMAPBackend) GetAttachment(ctx context.Context, messageID, attachmentID string) (*Attachment, error) {
	if b.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Parse attachment index
	var attachIndex int
	if _, err := fmt.Sscanf(attachmentID, "%d", &attachIndex); err != nil {
		return nil, fmt.Errorf("invalid attachment ID: %s", attachmentID)
	}

	// Parse sequence number
	var seqNum uint32
	if _, err := fmt.Sscanf(messageID, "%d", &seqNum); err != nil {
		return nil, fmt.Errorf("invalid message ID: %s", messageID)
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(seqNum)

	// Fetch full message
	messages := make(chan *imap.Message, 1)
	section := &imap.BodySectionName{}
	done := make(chan error, 1)

	go func() {
		done <- b.client.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	r := msg.GetBody(section)
	if r == nil {
		return nil, fmt.Errorf("failed to get message body")
	}

	mr, err := mail.CreateReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to create mail reader: %w", err)
	}

	partIndex := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading part: %w", err)
		}

		if h, ok := p.Header.(*mail.AttachmentHeader); ok {
			if partIndex == attachIndex {
				filename, _ := h.Filename()
				contentType, _, _ := h.ContentType()
				body, err := io.ReadAll(p.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read attachment body: %w", err)
				}

				return &Attachment{
					AttachmentInfo: AttachmentInfo{
						AttachmentID: attachmentID,
						Filename:     filename,
						ContentType:  contentType,
						SizeBytes:    int64(len(body)),
					},
					Data: body,
				}, nil
			}
		}
		partIndex++
	}

	return nil, fmt.Errorf("attachment not found: %s", attachmentID)
}

// Helper functions

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

func hasAttachmentInStructure(bs *imap.BodyStructure) bool {
	if bs == nil {
		return false
	}
	if strings.EqualFold(bs.Disposition, "attachment") {
		return true
	}
	for _, part := range bs.Parts {
		if hasAttachmentInStructure(part) {
			return true
		}
	}
	return false
}
