package moneyfx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// ErrLockNotFound is returned when a rate lock cannot be found.
var ErrLockNotFound = errors.New("moneyfx: rate lock not found")

// LockID identifies an immutable stored FX rate lock.
type LockID int64

// LockRef identifies the external object that owns a lock. Module is the
// owning Ledgerly module and Ref is its opaque object reference.
type LockRef struct {
	Module string
	Ref    string
}

// String returns the canonical module:ref representation used in storage.
func (r LockRef) String() string {
	_, refText, err := normalizeLockRef(r)
	if err != nil {
		module := strings.TrimSpace(r.Module)
		ref := strings.TrimSpace(r.Ref)
		if module == "" {
			return ref
		}
		if ref == "" {
			return module
		}
		return module + ":" + ref
	}
	return refText
}

// RateLock is an immutable FX rate snapshot for a module-owned reference.
type RateLock struct {
	ID       LockID
	Ref      LockRef
	From     string
	To       string
	Rate     string
	RateDate time.Time
	LockedAt time.Time
	Source   string
}

type rateLockStore interface {
	InsertRateLock(ctx context.Context, tx db.Tx, lock newRateLock) (RateLock, error)
	RateLockByID(ctx context.Context, id LockID) (RateLock, error)
	ActiveRateLockFor(ctx context.Context, ref LockRef) (RateLock, error)
}

type newRateLock struct {
	Ref      LockRef
	RefText  string
	From     string
	To       string
	Rate     string
	RateDate time.Time
	LockedAt time.Time
	Source   string
}

// Lock resolves and appends an immutable ECB rate lock inside the caller's
// transaction.
func (s *Service) Lock(ctx context.Context, tx db.Tx, ref LockRef, from string, to string, date time.Time) (RateLock, error) {
	if err := ctx.Err(); err != nil {
		return RateLock{}, err
	}
	if tx == nil {
		return RateLock{}, fmt.Errorf("moneyfx: lock requires transaction")
	}
	if s == nil || s.locks == nil {
		return RateLock{}, fmt.Errorf("moneyfx: rate lock store is required")
	}
	normalizedRef, refText, err := normalizeLockRef(ref)
	if err != nil {
		return RateLock{}, err
	}

	rateService := &Service{
		store: txRateReader{tx: tx},
		clock: s.clock,
	}
	rate, err := rateService.RateOn(ctx, date, from, to)
	if err != nil {
		return RateLock{}, err
	}
	if rate.Source != rateSourceECB {
		return RateLock{}, fmt.Errorf("moneyfx: lock requires ECB rate source, got %q", rate.Source)
	}
	decimal, err := canonicalRateLockDecimal(rate.Value)
	if err != nil {
		return RateLock{}, fmt.Errorf("moneyfx: lock rate: %w", err)
	}

	rateDate := normalizeRateDate(rate.RateDate)
	if rateDate.IsZero() {
		return RateLock{}, fmt.Errorf("moneyfx: lock rate date is required")
	}
	return s.locks.InsertRateLock(ctx, tx, newRateLock{
		Ref:      normalizedRef,
		RefText:  refText,
		From:     rate.From,
		To:       rate.To,
		Rate:     decimal,
		RateDate: rateDate,
		LockedAt: s.nowUTC(),
		Source:   rateSourceECB,
	})
}

// GetLock returns a stored immutable rate lock by id.
func (s *Service) GetLock(ctx context.Context, id LockID) (RateLock, error) {
	if err := ctx.Err(); err != nil {
		return RateLock{}, err
	}
	if s == nil || s.locks == nil {
		return RateLock{}, fmt.Errorf("moneyfx: rate lock store is required")
	}
	return s.locks.RateLockByID(ctx, id)
}

// ActiveLockFor returns the newest lock row for ref.
func (s *Service) ActiveLockFor(ctx context.Context, ref LockRef) (RateLock, error) {
	if err := ctx.Err(); err != nil {
		return RateLock{}, err
	}
	if s == nil || s.locks == nil {
		return RateLock{}, fmt.Errorf("moneyfx: rate lock store is required")
	}
	return s.locks.ActiveRateLockFor(ctx, ref)
}

func normalizeLockRef(ref LockRef) (LockRef, string, error) {
	module := strings.ToLower(strings.TrimSpace(ref.Module))
	value := strings.TrimSpace(ref.Ref)
	if module == "" {
		return LockRef{}, "", fmt.Errorf("moneyfx: lock ref module is required")
	}
	if value == "" {
		return LockRef{}, "", fmt.Errorf("moneyfx: lock ref is required")
	}
	if strings.Contains(module, ":") {
		return LockRef{}, "", fmt.Errorf("moneyfx: lock ref module %q contains ':'", module)
	}
	for _, r := range module {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return LockRef{}, "", fmt.Errorf("moneyfx: lock ref module %q contains invalid character %q", module, r)
		}
	}
	normalized := LockRef{Module: module, Ref: value}
	return normalized, module + ":" + value, nil
}

func parseLockRef(refText string) (LockRef, error) {
	refText = strings.TrimSpace(refText)
	separator := strings.IndexByte(refText, ':')
	if separator <= 0 || separator == len(refText)-1 {
		return LockRef{}, fmt.Errorf("moneyfx: stored lock ref %q is invalid", refText)
	}
	ref, _, err := normalizeLockRef(LockRef{
		Module: refText[:separator],
		Ref:    refText[separator+1:],
	})
	return ref, err
}

type txRateReader struct {
	tx db.Tx
}

func (r txRateReader) ECBRate(ctx context.Context, date time.Time, currency string) (StoredECBRate, error) {
	return loadECBRate(ctx, r.tx, date, currency)
}

func (r txRateReader) ECBRateDateOnOrBefore(ctx context.Context, date time.Time, minDate time.Time, currencies []string) (time.Time, bool, error) {
	return loadECBRateDateOnOrBefore(ctx, r.tx, date, minDate, currencies)
}

func (r txRateReader) LatestRateDate(ctx context.Context) (time.Time, bool, error) {
	return loadLatestRateDate(ctx, r.tx)
}
