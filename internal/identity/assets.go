package identity

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DataDirEnv is the filesystem root used for persisted Ledgerly assets.
	DataDirEnv = "LEDGERLY_DATA_DIR"

	// MaxLogoAssetBytes is the largest accepted replaceable company logo.
	MaxLogoAssetBytes = 2 * 1024 * 1024

	DevSeedLogoAssetID AssetID = "17830098-8109-4a00-8b00-000000000001"
	devSeedLogoSHA256          = "8d2bd59537987e78dd8259ad3b12b3a897e0eabb1306b05c2aa6a93cb51b1948"
	devSeedLogoMIME            = "image/png"
	devSeedLogoSize    int64   = 508905
)

var errDataDirRequired = fmt.Errorf("identity: %s is required", DataDirEnv)

type validatedLogoAsset struct {
	sha256 string
	mime   string
	size   int64
	bytes  []byte
}

type fileAssetStore struct {
	dataDir string
}

func fileAssetStoreFromEnv() fileAssetStore {
	return fileAssetStore{dataDir: os.Getenv(DataDirEnv)}
}

func (s fileAssetStore) write(sha string, data []byte) error {
	assetDir, err := s.assetDir()
	if err != nil {
		return err
	}
	if !isSHA256Hex(sha) {
		return fmt.Errorf("identity: invalid asset sha256 %q", sha)
	}
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		return fmt.Errorf("create asset directory: %w", err)
	}

	path := filepath.Join(assetDir, sha)
	if err := verifyAssetFile(path, sha); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(assetDir, "."+sha+".tmp-*")
	if err != nil {
		return fmt.Errorf("create asset temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write asset temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close asset temp file: %w", err)
	}
	if err := verifyAssetFile(tmpPath, sha); err != nil {
		return err
	}

	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return verifyAssetFile(path, sha)
		}
		return fmt.Errorf("install asset file: %w", err)
	}
	return nil
}

func (s fileAssetStore) read(sha string) ([]byte, error) {
	assetDir, err := s.assetDir()
	if err != nil {
		return nil, err
	}
	if !isSHA256Hex(sha) {
		return nil, fmt.Errorf("identity: invalid asset sha256 %q", sha)
	}

	path := filepath.Join(assetDir, sha)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read asset file: %w", err)
	}
	if got := sha256Hex(data); got != sha {
		return nil, fmt.Errorf("identity: asset file %s sha256 = %s, want %s", path, got, sha)
	}
	return data, nil
}

func (s fileAssetStore) assetDir() (string, error) {
	dataDir := strings.TrimSpace(s.dataDir)
	if dataDir == "" {
		return "", errDataDirRequired
	}
	return filepath.Join(dataDir, "assets"), nil
}

func verifyAssetFile(path string, sha string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash asset file: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != sha {
		return fmt.Errorf("identity: existing asset file %s sha256 = %s, want %s", path, got, sha)
	}
	return nil
}

func validateLogoUpload(upload LogoUpload) (validatedLogoAsset, error) {
	if len(upload.Bytes) > MaxLogoAssetBytes {
		return validatedLogoAsset{}, ErrAssetTooLarge
	}

	mediaType, err := normalizeAssetMIME(upload.MIME)
	if err != nil {
		return validatedLogoAsset{}, err
	}
	if !logoBytesMatchMIME(upload.Bytes, mediaType) {
		return validatedLogoAsset{}, fmt.Errorf("%w: %s", ErrUnsupportedAsset, mediaType)
	}

	return validatedLogoAsset{
		sha256: sha256Hex(upload.Bytes),
		mime:   mediaType,
		size:   int64(len(upload.Bytes)),
		bytes:  append([]byte{}, upload.Bytes...),
	}, nil
}

func normalizeAssetMIME(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.TrimSpace(value)
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "image/png", "image/jpeg", "image/svg+xml":
		return mediaType, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedAsset, mediaType)
	}
}

func logoBytesMatchMIME(data []byte, mediaType string) bool {
	switch mediaType {
	case "image/png":
		_, format, err := image.DecodeConfig(bytes.NewReader(data))
		return err == nil && format == "png"
	case "image/jpeg":
		_, format, err := image.DecodeConfig(bytes.NewReader(data))
		return err == nil && format == "jpeg"
	case "image/svg+xml":
		return isSVG(data)
	default:
		return false
	}
}

func isSVG(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok {
			return start.Name.Local == "svg"
		}
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func newAssetID() (AssetID, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate asset id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return AssetID(fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		raw[0:4],
		raw[4:6],
		raw[6:8],
		raw[8:10],
		raw[10:16],
	)), nil
}
