package banking

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"path"
	"strings"
)

func validateReceiptUpload(upload ReceiptUpload) (ReceiptUpload, error) {
	if len(upload.Bytes) == 0 {
		return ReceiptUpload{}, fmt.Errorf("banking: receipt file is required: %w", ErrInvalidReceipt)
	}
	if len(upload.Bytes) > MaxReceiptBytes {
		return ReceiptUpload{}, ErrReceiptTooLarge
	}
	mediaType, err := normalizeReceiptMIME(upload.MIME)
	if err != nil {
		return ReceiptUpload{}, err
	}
	data := append([]byte{}, upload.Bytes...)
	if !receiptBytesMatchMIME(data, mediaType) {
		return ReceiptUpload{}, fmt.Errorf("%w: %s", ErrUnsupportedReceipt, mediaType)
	}
	return ReceiptUpload{
		Filename: normalizeReceiptFilename(upload.Filename, mediaType),
		MIME:     mediaType,
		Bytes:    data,
	}, nil
}

func normalizeReceiptMIME(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.TrimSpace(value)
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "application/pdf", "image/png", "image/jpeg":
		return mediaType, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedReceipt, mediaType)
	}
}

func receiptBytesMatchMIME(data []byte, mediaType string) bool {
	switch mediaType {
	case "application/pdf":
		return bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-"))
	case "image/png":
		_, format, err := image.DecodeConfig(bytes.NewReader(data))
		return err == nil && format == "png"
	case "image/jpeg":
		_, format, err := image.DecodeConfig(bytes.NewReader(data))
		return err == nil && format == "jpeg"
	default:
		return false
	}
}

func normalizeReceiptFilename(filename string, mediaType string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	normalized = path.Base(normalized)
	if normalized == "." || normalized == "/" || normalized == "" {
		return "receipt" + receiptExtension(mediaType)
	}
	return strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, normalized)
}

func receiptExtension(mediaType string) string {
	switch mediaType {
	case "application/pdf":
		return ".pdf"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	default:
		return ""
	}
}
