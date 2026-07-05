package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const dateLayout = "2006-01-02"

// ErrProfileNotFound is returned when the single profile row has not been
// initialised yet.
var ErrProfileNotFound = errors.New("identity: company profile not found")

// Service implements the identity module API.
type Service struct {
	tx    db.Tx
	bus   *bus.Bus
	store profileStore
}

// New returns an identity API bound to tx. The caller owns the transaction
// lifetime; UpdateProfile publishes its event on the supplied transaction.
func New(tx db.Tx, eventBus *bus.Bus) *Service {
	return &Service{
		tx:    tx,
		bus:   eventBus,
		store: profileStore{},
	}
}

// Profile returns the current company profile.
func (s *Service) Profile(ctx context.Context) (CompanyProfile, error) {
	return s.store.profile(ctx, s.tx)
}

// UpdateProfile applies a partial profile update and publishes ProfileUpdated
// inside the same caller-owned transaction.
func (s *Service) UpdateProfile(ctx context.Context, patch UpdateProfilePatch) error {
	profile, err := s.store.profileForUpdate(ctx, s.tx)
	if err != nil {
		return err
	}

	updated, err := patch.apply(profile)
	if err != nil {
		return err
	}
	if err := s.store.updateProfile(ctx, s.tx, updated); err != nil {
		return err
	}
	if s.bus == nil {
		return nil
	}
	if err := s.bus.Publish(ctx, s.tx, ProfileUpdated{}); err != nil {
		return fmt.Errorf("publish profile updated: %w", err)
	}
	return nil
}

// CompanyFacts returns identity facts consumed by jurisdiction and reports.
func (s *Service) CompanyFacts(ctx context.Context) (CompanyFacts, error) {
	profile, err := s.Profile(ctx)
	if err != nil {
		return CompanyFacts{}, err
	}
	return CompanyFacts{
		IncorporationDate: profile.IncorporationDate,
		YearEnd:           profile.YearEnd,
	}, nil
}

func (patch UpdateProfilePatch) apply(profile CompanyProfile) (CompanyProfile, error) {
	if patch.TradingName != nil {
		profile.TradingName = *patch.TradingName
	}
	if patch.LegalName != nil {
		profile.LegalName = *patch.LegalName
	}
	if patch.CompanyNumber != nil {
		companyNumber := strings.TrimSpace(*patch.CompanyNumber)
		if companyNumber == "" {
			return CompanyProfile{}, fmt.Errorf("identity: company number is required")
		}
		profile.CompanyNumber = companyNumber
	}
	if patch.RegisteredOffice != nil {
		profile.RegisteredOffice = *patch.RegisteredOffice
	}
	if patch.IncorporationDate != nil {
		incorporationDate, err := parseDate(*patch.IncorporationDate)
		if err != nil {
			return CompanyProfile{}, err
		}
		profile.IncorporationDate = incorporationDate
	}
	if patch.YearEnd != nil {
		if err := patch.YearEnd.validate(); err != nil {
			return CompanyProfile{}, err
		}
		profile.YearEnd = *patch.YearEnd
	}
	if patch.VATNumber != nil {
		vatNumber := strings.TrimSpace(*patch.VATNumber)
		if vatNumber == "" {
			profile.VATNumber = nil
		} else {
			profile.VATNumber = &vatNumber
		}
	}
	if patch.BankDetails != nil {
		profile.BankDetails = *patch.BankDetails
	}
	if patch.Shareholders != nil {
		profile.Shareholders = append([]Shareholder(nil), (*patch.Shareholders)...)
	}
	if patch.LogoAssetID != nil {
		logoAssetID := AssetID(strings.TrimSpace(string(*patch.LogoAssetID)))
		if logoAssetID == "" {
			profile.LogoAssetID = nil
		} else {
			profile.LogoAssetID = &logoAssetID
		}
	}

	if strings.TrimSpace(profile.CompanyNumber) == "" {
		return CompanyProfile{}, fmt.Errorf("identity: company number is required")
	}
	if err := profile.YearEnd.validate(); err != nil {
		return CompanyProfile{}, err
	}
	return profile, nil
}

func parseDate(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("identity: date is required")
	}
	parsed, err := time.Parse(dateLayout, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("identity: parse date %q as YYYY-MM-DD: %w", trimmed, err)
	}
	return parsed, nil
}

func (yearEnd YearEnd) validate() error {
	month := int(yearEnd.Month)
	if month < 1 || month > 12 {
		return fmt.Errorf("identity: year-end month %d out of range", month)
	}
	if yearEnd.Day < 1 || yearEnd.Day > daysInMonth(yearEnd.Month) {
		return fmt.Errorf("identity: year-end day %d out of range for month %d", yearEnd.Day, month)
	}
	return nil
}

func daysInMonth(month time.Month) int {
	switch month {
	case time.April, time.June, time.September, time.November:
		return 30
	case time.February:
		return 29
	default:
		return 31
	}
}
