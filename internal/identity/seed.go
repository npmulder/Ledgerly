package identity

import (
	"context"
	"fmt"
	"os"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// SeedDevLogoAsset installs the design-handoff logo into the content-addressed
// asset store and ensures the dev/test profile points at the seeded asset.
func SeedDevLogoAsset(ctx context.Context, tx db.Tx, dataDir string, sourcePath string) (AssetID, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read dev seed logo: %w", err)
	}
	validated, err := validateLogoUpload(LogoUpload{MIME: devSeedLogoMIME, Bytes: data})
	if err != nil {
		return "", err
	}
	if validated.sha256 != devSeedLogoSHA256 || validated.size != devSeedLogoSize {
		return "", fmt.Errorf(
			"identity: dev seed logo sha256/size = %s/%d, want %s/%d",
			validated.sha256,
			validated.size,
			devSeedLogoSHA256,
			devSeedLogoSize,
		)
	}

	if err := (fileAssetStore{dataDir: dataDir}).write(validated.sha256, validated.bytes); err != nil {
		return "", err
	}

	store := profileStore{}
	record := assetRecord{
		ID:     DevSeedLogoAssetID,
		SHA256: validated.sha256,
		MIME:   validated.mime,
		Size:   validated.size,
	}
	if err := store.ensureAsset(ctx, tx, record); err != nil {
		return "", err
	}
	stored, err := store.asset(ctx, tx, DevSeedLogoAssetID)
	if err != nil {
		return "", err
	}
	if stored.SHA256 != record.SHA256 || stored.MIME != record.MIME || stored.Size != record.Size {
		return "", fmt.Errorf("identity: existing dev seed asset metadata does not match handoff logo")
	}
	if err := store.setProfileLogoAssetIDIfEmpty(ctx, tx, DevSeedLogoAssetID); err != nil {
		return "", err
	}
	return DevSeedLogoAssetID, nil
}
