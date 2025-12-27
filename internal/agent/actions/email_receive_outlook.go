//go:build windows
// +build windows

// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"github.com/rs/zerolog"
)

// OutlookBackend implements EmailBackend using Outlook COM automation.
type OutlookBackend struct {
	config  EmailReceiveConfig
	logger  zerolog.Logger
	outlook *ole.IDispatch
	ns      *ole.IDispatch
}

// newOutlookBackend creates a new Outlook backend (Windows only).
func newOutlookBackend(cfg EmailReceiveConfig, logger zerolog.Logger) EmailBackend {
	if !cfg.OutlookEnabled {
		return nil
	}
	return &OutlookBackend{
		config: cfg,
		logger: logger.With().Str("backend", "outlook").Logger(),
	}
}

// Name returns the backend name.
func (b *OutlookBackend) Name() string {
	return "outlook"
}

// Connect initializes COM and connects to Outlook.
func (b *OutlookBackend) Connect(ctx context.Context, config *EmailBackendConfig) error {
	if err := ole.CoInitialize(0); err != nil {
		// May already be initialized in this thread
		b.logger.Debug().Err(err).Msg("CoInitialize (may already be initialized)")
	}

	unknown, err := oleutil.CreateObject("Outlook.Application")
	if err != nil {
		ole.CoUninitialize()
		return fmt.Errorf("failed to create Outlook.Application: %w", err)
	}

	b.outlook, err = unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		ole.CoUninitialize()
		return fmt.Errorf("failed to query interface: %w", err)
	}

	// Get MAPI namespace
	nsDisp, err := oleutil.CallMethod(b.outlook, "GetNamespace", "MAPI")
	if err != nil {
		b.outlook.Release()
		ole.CoUninitialize()
		return fmt.Errorf("failed to get MAPI namespace: %w", err)
	}
	b.ns = nsDisp.ToIDispatch()

	b.logger.Debug().Msg("Outlook COM connected")
	return nil
}

// Disconnect releases COM objects.
func (b *OutlookBackend) Disconnect() error {
	if b.ns != nil {
		b.ns.Release()
		b.ns = nil
	}
	if b.outlook != nil {
		b.outlook.Release()
		b.outlook = nil
	}
	ole.CoUninitialize()
	return nil
}

// ListEmails returns emails matching the filter.
func (b *OutlookBackend) ListEmails(ctx context.Context, filter *EmailFilter) ([]*EmailHeader, error) {
	if b.ns == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Get folder constant
	folderConst := b.getFolderConstant(filter.Folder)

	folderDisp, err := oleutil.CallMethod(b.ns, "GetDefaultFolder", folderConst)
	if err != nil {
		return nil, fmt.Errorf("GetDefaultFolder failed: %w", err)
	}
	folder := folderDisp.ToIDispatch()
	defer folder.Release()

	// Get Items collection
	itemsDisp, err := oleutil.GetProperty(folder, "Items")
	if err != nil {
		return nil, fmt.Errorf("get Items failed: %w", err)
	}
	items := itemsDisp.ToIDispatch()
	defer items.Release()

	// Sort by ReceivedTime descending (newest first)
	oleutil.CallMethod(items, "Sort", "[ReceivedTime]", true)

	// Build and apply restriction filter
	restriction := b.buildRestriction(filter)
	var filteredItems *ole.IDispatch
	if restriction != "" {
		restrictedDisp, err := oleutil.CallMethod(items, "Restrict", restriction)
		if err != nil {
			b.logger.Warn().Str("filter", restriction).Err(err).Msg("Restrict failed, using unfiltered")
			filteredItems = items
		} else {
			filteredItems = restrictedDisp.ToIDispatch()
			defer filteredItems.Release()
		}
	} else {
		filteredItems = items
	}

	// Get count
	countDisp, err := oleutil.GetProperty(filteredItems, "Count")
	if err != nil {
		return nil, fmt.Errorf("get Count failed: %w", err)
	}
	count := int(countDisp.Val)

	maxResults := filter.MaxResults
	if maxResults <= 0 || maxResults > count {
		maxResults = count
	}
	if b.config.MaxEmailsPerQuery > 0 && maxResults > b.config.MaxEmailsPerQuery {
		maxResults = b.config.MaxEmailsPerQuery
	}

	headers := make([]*EmailHeader, 0, maxResults)
	for i := 1; i <= maxResults; i++ {
		select {
		case <-ctx.Done():
			return headers, ctx.Err()
		default:
		}

		itemDisp, err := oleutil.CallMethod(filteredItems, "Item", i)
		if err != nil {
			continue
		}
		item := itemDisp.ToIDispatch()

		header := b.parseEmailHeader(item)
		item.Release()

		if header == nil {
			continue
		}

		// Apply additional filters not supported by Restrict
		if filter.HasAttachment != nil && *filter.HasAttachment != header.HasAttachment {
			continue
		}

		headers = append(headers, header)
	}

	return headers, nil
}

// ReadEmail retrieves full email content.
func (b *OutlookBackend) ReadEmail(ctx context.Context, messageID string) (*Email, error) {
	if b.ns == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Use GetItemFromID to retrieve by EntryID
	itemDisp, err := oleutil.CallMethod(b.ns, "GetItemFromID", messageID)
	if err != nil {
		return nil, fmt.Errorf("GetItemFromID failed: %w", err)
	}
	item := itemDisp.ToIDispatch()
	defer item.Release()

	header := b.parseEmailHeader(item)
	if header == nil {
		return nil, fmt.Errorf("failed to parse email header")
	}

	body, _ := oleutil.GetProperty(item, "Body")
	bodyHTML, _ := oleutil.GetProperty(item, "HTMLBody")

	// Get attachments info
	attachDisp, err := oleutil.GetProperty(item, "Attachments")
	if err != nil {
		return nil, fmt.Errorf("get Attachments failed: %w", err)
	}
	attachments := attachDisp.ToIDispatch()
	defer attachments.Release()

	countDisp, _ := oleutil.GetProperty(attachments, "Count")
	count := int(countDisp.Val)

	attachInfos := make([]AttachmentInfo, 0, count)
	for i := 1; i <= count; i++ {
		attDisp, err := oleutil.CallMethod(attachments, "Item", i)
		if err != nil {
			continue
		}
		att := attDisp.ToIDispatch()

		filename, _ := oleutil.GetProperty(att, "FileName")
		size, _ := oleutil.GetProperty(att, "Size")
		attType, _ := oleutil.GetProperty(att, "Type")

		// Type 1 = olByValue (regular attachment)
		// Type 5 = olEmbeddedItem (embedded)
		// Type 6 = olOLE (OLE object)
		contentType := "application/octet-stream"
		if attType.Val == 1 {
			contentType = b.guessContentType(filename.ToString())
		}

		attachInfos = append(attachInfos, AttachmentInfo{
			AttachmentID: fmt.Sprintf("%d", i),
			Filename:     filename.ToString(),
			ContentType:  contentType,
			SizeBytes:    int64(size.Val),
		})
		att.Release()
	}

	return &Email{
		EmailHeader: *header,
		Body:        body.ToString(),
		BodyHTML:    bodyHTML.ToString(),
		Attachments: attachInfos,
	}, nil
}

// GetAttachment retrieves a specific attachment.
func (b *OutlookBackend) GetAttachment(ctx context.Context, messageID, attachmentID string) (*Attachment, error) {
	if b.ns == nil {
		return nil, fmt.Errorf("not connected")
	}

	itemDisp, err := oleutil.CallMethod(b.ns, "GetItemFromID", messageID)
	if err != nil {
		return nil, fmt.Errorf("GetItemFromID failed: %w", err)
	}
	item := itemDisp.ToIDispatch()
	defer item.Release()

	attachDisp, err := oleutil.GetProperty(item, "Attachments")
	if err != nil {
		return nil, fmt.Errorf("get Attachments failed: %w", err)
	}
	attachments := attachDisp.ToIDispatch()
	defer attachments.Release()

	// attachmentID is the 1-based index
	idx := 1
	fmt.Sscanf(attachmentID, "%d", &idx)

	attDisp, err := oleutil.CallMethod(attachments, "Item", idx)
	if err != nil {
		return nil, fmt.Errorf("get attachment %d failed: %w", idx, err)
	}
	att := attDisp.ToIDispatch()
	defer att.Release()

	filename, _ := oleutil.GetProperty(att, "FileName")
	size, _ := oleutil.GetProperty(att, "Size")

	// Save to temp file to get content
	tempDir := os.TempDir()
	tempPath := filepath.Join(tempDir, fmt.Sprintf("cymbytes_att_%d_%s", time.Now().UnixNano(), filename.ToString()))

	_, err = oleutil.CallMethod(att, "SaveAsFile", tempPath)
	if err != nil {
		return nil, fmt.Errorf("SaveAsFile failed: %w", err)
	}
	defer os.Remove(tempPath)

	data, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, fmt.Errorf("read temp file failed: %w", err)
	}

	return &Attachment{
		AttachmentInfo: AttachmentInfo{
			AttachmentID: attachmentID,
			Filename:     filename.ToString(),
			ContentType:  b.guessContentType(filename.ToString()),
			SizeBytes:    int64(size.Val),
		},
		Data: data,
	}, nil
}

// Helper functions

func (b *OutlookBackend) getFolderConstant(folder string) int {
	// Outlook folder constants
	switch strings.ToUpper(folder) {
	case "INBOX":
		return 6 // olFolderInbox
	case "SENT", "SENT ITEMS":
		return 5 // olFolderSentMail
	case "DRAFTS":
		return 16 // olFolderDrafts
	case "DELETED", "DELETED ITEMS", "TRASH":
		return 3 // olFolderDeletedItems
	case "OUTBOX":
		return 4 // olFolderOutbox
	case "JUNK", "SPAM", "JUNK EMAIL":
		return 23 // olFolderJunk
	default:
		return 6 // Default to inbox
	}
}

func (b *OutlookBackend) parseEmailHeader(item *ole.IDispatch) *EmailHeader {
	subject, _ := oleutil.GetProperty(item, "Subject")
	sender, _ := oleutil.GetProperty(item, "SenderEmailAddress")
	received, _ := oleutil.GetProperty(item, "ReceivedTime")
	unread, _ := oleutil.GetProperty(item, "UnRead")
	entryID, _ := oleutil.GetProperty(item, "EntryID")

	// Check for attachments
	attachDisp, err := oleutil.GetProperty(item, "Attachments")
	if err != nil {
		return nil
	}
	attachments := attachDisp.ToIDispatch()
	attachCountDisp, _ := oleutil.GetProperty(attachments, "Count")
	attachCount := int(attachCountDisp.Val)
	attachments.Release()

	// Parse received time (OLE DATE format)
	var receivedAt time.Time
	if received.Val != 0 {
		// OLE Automation date: days since December 30, 1899
		oleDate := received.Value().(float64)
		receivedAt = oleAutomationDateToTime(oleDate)
	}

	unreadBool := false
	if unread.Val != 0 {
		unreadBool = true
	}

	return &EmailHeader{
		MessageID:     entryID.ToString(),
		Subject:       subject.ToString(),
		Sender:        sender.ToString(),
		ReceivedAt:    receivedAt,
		HasAttachment: attachCount > 0,
		IsRead:        !unreadBool,
	}
}

func (b *OutlookBackend) buildRestriction(filter *EmailFilter) string {
	parts := []string{}

	if filter.Subject != "" {
		// Use DASL query for subject contains
		parts = append(parts, fmt.Sprintf("@SQL=\"urn:schemas:httpmail:subject\" LIKE '%%%s%%'", filter.Subject))
	}
	if filter.Sender != "" {
		parts = append(parts, fmt.Sprintf("@SQL=\"urn:schemas:httpmail:senderemail\" LIKE '%%%s%%'", filter.Sender))
	}
	if !filter.Since.IsZero() {
		parts = append(parts, fmt.Sprintf("[ReceivedTime] >= '%s'", filter.Since.Format("01/02/2006 3:04 PM")))
	}
	if !filter.Before.IsZero() {
		parts = append(parts, fmt.Sprintf("[ReceivedTime] <= '%s'", filter.Before.Format("01/02/2006 3:04 PM")))
	}
	if filter.Unread != nil && *filter.Unread {
		parts = append(parts, "[UnRead] = True")
	}

	if len(parts) == 0 {
		return ""
	}

	// Combine with AND
	return strings.Join(parts, " AND ")
}

func (b *OutlookBackend) guessContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".txt":
		return "text/plain"
	case ".html", ".htm":
		return "text/html"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".zip":
		return "application/zip"
	case ".exe":
		return "application/x-msdownload"
	default:
		return "application/octet-stream"
	}
}

// oleAutomationDateToTime converts OLE Automation date to Go time.Time
func oleAutomationDateToTime(oleDate float64) time.Time {
	// OLE date epoch is December 30, 1899
	// Days are the integer part, time is the fractional part
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	duration := time.Duration(oleDate * 24 * float64(time.Hour))
	return epoch.Add(duration)
}
