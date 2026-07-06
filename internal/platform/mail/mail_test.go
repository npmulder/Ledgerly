package mail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"testing"
)

func TestBuildMessageBytesWrappedAttachmentDecodesToOriginalBytes(t *testing.T) {
	pdf := []byte("%PDF-1.4\n" + strings.Repeat("0123456789abcdef", 24))
	raw, err := buildMessageBytes("billing@example.test", Message{
		To:       "accounts@example.test",
		Subject:  "Payment reminder",
		TextBody: "Hello,\nPlease see the attached invoice.\n",
		Attachments: []Attachment{{
			Filename:    "INV-2025-01.pdf",
			ContentType: "application/pdf",
			Bytes:       pdf,
		}},
	})
	if err != nil {
		t.Fatalf("buildMessageBytes() error = %v", err)
	}

	attachmentBody := attachmentPartBody(t, raw)
	if !bytes.Contains(attachmentBody, []byte("\r\n")) {
		t.Fatalf("attachment body is not wrapped:\n%s", string(attachmentBody))
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(attachmentBody)))
	if err != nil {
		t.Fatalf("decode wrapped attachment: %v", err)
	}
	if !bytes.Equal(decoded, pdf) {
		t.Fatalf("decoded attachment bytes mismatch\n got: %q\nwant: %q", decoded, pdf)
	}
}

func attachmentPartBody(t *testing.T, raw []byte) []byte {
	t.Helper()

	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read MIME message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("content type = %q, want multipart/mixed", mediaType)
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read MIME part: %v", err)
		}
		if disposition, _, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition")); disposition == "attachment" {
			body, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read attachment part: %v", err)
			}
			return body
		}
	}
	t.Fatal("attachment part not found")
	return nil
}
