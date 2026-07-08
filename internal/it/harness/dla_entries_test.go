package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const dlaCashAccount ledger.AccountCode = "1000-cash-gbp"

func TestDLAEntriesPostLedgerShapesAndRunningBalance(t *testing.T) {
	fixture := newDLAFixture(t)
	sameDay := time.Date(2026, 7, 1, 11, 15, 0, 0, time.UTC)
	nextDay := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:txn-100",
		Date:            sameDay,
		Amount:          gbp(10_000),
		CashAccountCode: dlaCashAccount,
	})
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:               sameDay,
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Software paid personally",
		Amount:             gbp(2_500),
		Source:             "manual:expense-1",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(expense-owed) error = %v", err)
	}
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:            nextDay,
		Kind:            dla.EntryKindRepayment,
		Description:     "Director repayment",
		Amount:          gbp(4_000),
		Source:          "manual:repayment-1",
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("AddEntry(repayment) error = %v", err)
	}

	entries, err := fixture.dla.Ledger(fixture.ctx, dla.LedgerFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Ledger() error = %v", err)
	}
	assertDLAEntries(t, entries, []wantDLAEntry{
		{
			kind:           dla.EntryKindDrawing,
			source:         "banking:txn-100",
			amount:         10_000,
			owedToYou:      0,
			drawn:          10_000,
			runningBalance: -10_000,
			side:           dla.BalanceSideDebit,
		},
		{
			kind:           dla.EntryKindExpenseOwed,
			source:         "manual:expense-1",
			amount:         2_500,
			owedToYou:      2_500,
			drawn:          0,
			runningBalance: -7_500,
			side:           dla.BalanceSideDebit,
		},
		{
			kind:           dla.EntryKindRepayment,
			source:         "manual:repayment-1",
			amount:         4_000,
			owedToYou:      4_000,
			drawn:          0,
			runningBalance: -3_500,
			side:           dla.BalanceSideDebit,
		},
	})
	if !entries[0].Date.Equal(entries[1].Date) || entries[0].ID >= entries[1].ID {
		t.Fatalf("same-day entries ordered by IDs = %d then %d on dates %s/%s",
			entries[0].ID,
			entries[1].ID,
			entries[0].Date,
			entries[1].Date,
		)
	}

	fixture.assertLedgerPostings(t, "banking:txn-100", []wantPosting{
		{account: dla.DLAAccountCode, amount: 10_000},
		{account: dlaCashAccount, amount: -10_000},
	})
	fixture.assertLedgerPostings(t, "manual:expense-1", []wantPosting{
		{account: "5010-software", amount: 2_500},
		{account: dla.DLAAccountCode, amount: -2_500},
	})
	fixture.assertLedgerPostings(t, "manual:repayment-1", []wantPosting{
		{account: dlaCashAccount, amount: 4_000},
		{account: dla.DLAAccountCode, amount: -4_000},
	})
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAEntriesAreIsolatedPerDirector(t *testing.T) {
	fixture := newDLAFixture(t)
	entryDate := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	secondDirector := dla.DirectorID("director-2")

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Director:        dla.DefaultDirectorID,
		Ref:             "banking:director-1-drawing",
		Date:            entryDate,
		Amount:          gbp(10_000),
		CashAccountCode: dlaCashAccount,
	})
	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Director:        secondDirector,
		Ref:             "banking:director-2-drawing",
		Date:            entryDate,
		Amount:          gbp(7_000),
		CashAccountCode: dlaCashAccount,
	})

	balanceOne, statusOne, err := fixture.dla.CurrentBalance(fixture.ctx, dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("CurrentBalance(director-1) error = %v", err)
	}
	if balanceOne != gbp(-10_000) || statusOne != dla.StatusOverdrawn {
		t.Fatalf("CurrentBalance(director-1) = %#v/%s, want -100 GBP overdrawn", balanceOne, statusOne)
	}
	balanceTwo, statusTwo, err := fixture.dla.CurrentBalance(fixture.ctx, secondDirector)
	if err != nil {
		t.Fatalf("CurrentBalance(director-2) error = %v", err)
	}
	if balanceTwo != gbp(-7_000) || statusTwo != dla.StatusOverdrawn {
		t.Fatalf("CurrentBalance(director-2) = %#v/%s, want -70 GBP overdrawn", balanceTwo, statusTwo)
	}

	directorOneEntries, err := fixture.dla.Ledger(fixture.ctx, dla.LedgerFilter{Director: dla.DefaultDirectorID, Limit: 10})
	if err != nil {
		t.Fatalf("Ledger(director-1) error = %v", err)
	}
	assertDLAEntries(t, directorOneEntries, []wantDLAEntry{
		{
			kind:           dla.EntryKindDrawing,
			source:         "banking:director-1-drawing",
			amount:         10_000,
			drawn:          10_000,
			runningBalance: -10_000,
			side:           dla.BalanceSideDebit,
		},
	})
	if directorOneEntries[0].Director != dla.DefaultDirectorID {
		t.Fatalf("director-1 ledger director = %q, want %q", directorOneEntries[0].Director, dla.DefaultDirectorID)
	}

	directorTwoEntries, err := fixture.dla.Ledger(fixture.ctx, dla.LedgerFilter{Director: secondDirector, Limit: 10})
	if err != nil {
		t.Fatalf("Ledger(director-2) error = %v", err)
	}
	assertDLAEntries(t, directorTwoEntries, []wantDLAEntry{
		{
			kind:           dla.EntryKindDrawing,
			source:         "banking:director-2-drawing",
			amount:         7_000,
			drawn:          7_000,
			runningBalance: -7_000,
			side:           dla.BalanceSideDebit,
		},
	})
	if directorTwoEntries[0].Director != secondDirector {
		t.Fatalf("director-2 ledger director = %q, want %q", directorTwoEntries[0].Director, secondDirector)
	}

	fixture.assertLedgerPostings(t, "banking:director-1-drawing", []wantPosting{
		{account: dla.DLAAccountCode, amount: 10_000},
		{account: dlaCashAccount, amount: -10_000},
	})
	fixture.assertLedgerPostings(t, "banking:director-2-drawing", []wantPosting{
		{account: "2300-directors-loan-2", amount: 7_000},
		{account: dlaCashAccount, amount: -7_000},
	})
	report, err := fixture.dla.CheckConsistency(fixture.ctx, fixture.harness.Clock.Now())
	if err != nil {
		t.Fatalf("CheckConsistency() report=%+v error=%v, want consistent", report, err)
	}
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAStatusPolicyAndTransitionEvents(t *testing.T) {
	t.Cleanup(func() {
		if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
			t.Fatalf("restore default jurisdiction pack: %v", err)
		}
	})
	fixture := newDLAFixture(t)
	entryDate := fixture.harness.Clock.Now()

	var wentOverdrawn []dla.WentOverdrawn
	fixture.harness.Bus.Subscribe(dla.WentOverdrawnName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		event := evt.(dla.WentOverdrawn)
		balance, err := (dla.Store{}).CurrentBalance(ctx, tx, dla.DefaultDirectorID)
		if err != nil {
			return err
		}
		if balance != event.Balance {
			return fmt.Errorf("event balance = %#v, transaction balance = %#v", event.Balance, balance)
		}
		wentOverdrawn = append(wentOverdrawn, event)
		return nil
	})

	var backInCredit []dla.BackInCredit
	fixture.harness.Bus.Subscribe(dla.BackInCreditName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		event := evt.(dla.BackInCredit)
		balance, err := (dla.Store{}).CurrentBalance(ctx, tx, dla.DefaultDirectorID)
		if err != nil {
			return err
		}
		if balance != event.Balance {
			return fmt.Errorf("event balance = %#v, transaction balance = %#v", event.Balance, balance)
		}
		backInCredit = append(backInCredit, event)
		return nil
	})

	assertCurrentBalance(t, fixture.dla, gbp(0), dla.StatusCredit)

	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:               entryDate,
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Personally paid equipment",
		Amount:             gbp(5_000),
		Source:             "manual:expense-credit-start",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(expense-owed) error = %v", err)
	}
	assertCurrentBalance(t, fixture.dla, gbp(5_000), dla.StatusCredit)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 0, 0)

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:cross-overdrawn",
		Date:            entryDate,
		Amount:          gbp(6_000),
		CashAccountCode: dlaCashAccount,
	})
	assertCurrentBalance(t, fixture.dla, gbp(-1_000), dla.StatusOverdrawn)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 1, 0)
	if wentOverdrawn[0].Balance != gbp(-1_000) {
		t.Fatalf("WentOverdrawn balance = %#v, want %#v", wentOverdrawn[0].Balance, gbp(-1_000))
	}

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:further-overdrawn",
		Date:            entryDate,
		Amount:          gbp(500),
		CashAccountCode: dlaCashAccount,
	})
	assertCurrentBalance(t, fixture.dla, gbp(-1_500), dla.StatusOverdrawn)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 1, 0)
	clearance, err := fixture.dla.SuggestedClearanceAmount(fixture.ctx, dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("SuggestedClearanceAmount() error = %v", err)
	}
	if clearance != gbp(1_500) {
		t.Fatalf("SuggestedClearanceAmount() = %#v, want %#v", clearance, gbp(1_500))
	}

	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:            entryDate,
		Kind:            dla.EntryKindRepayment,
		Description:     "Director clears balance",
		Amount:          gbp(1_500),
		Source:          "manual:repayment-back-to-zero",
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("AddEntry(repayment) error = %v", err)
	}
	assertCurrentBalance(t, fixture.dla, gbp(0), dla.StatusCredit)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 1, 1)
	if backInCredit[0].Balance != gbp(0) {
		t.Fatalf("BackInCredit balance = %#v, want %#v", backInCredit[0].Balance, gbp(0))
	}
	clearance, err = fixture.dla.SuggestedClearanceAmount(fixture.ctx, dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("SuggestedClearanceAmount() after zero error = %v", err)
	}
	if clearance != gbp(0) {
		t.Fatalf("SuggestedClearanceAmount() after zero = %#v, want %#v", clearance, gbp(0))
	}

	status, err := fixture.dla.CurrentStatus(fixture.ctx, dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("CurrentStatus() error = %v", err)
	}
	if status.Policy.S455Charge ||
		status.Policy.BIKWarningTextKey != "benefit_in_kind_interest_free" ||
		status.Policy.Remedy != "clear_with_dividend" {
		t.Fatalf("CurrentStatus() policy = %#v, want Isle of Man DLA policy", status.Policy)
	}
	loadDirectorLoanPolicyPack(t, "fixture_changed_warning", "fixture_changed_remedy", true)
	status, err = fixture.dla.CurrentStatus(fixture.ctx, dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("CurrentStatus() changed pack error = %v", err)
	}
	if !status.Policy.S455Charge ||
		status.Policy.BIKWarningTextKey != "fixture_changed_warning" ||
		status.Policy.Remedy != "fixture_changed_remedy" {
		t.Fatalf("CurrentStatus() changed policy = %#v, want fixture pack values", status.Policy)
	}

	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAConsistencyCheckHealthzAndRecovery(t *testing.T) {
	var logs bytes.Buffer
	fixture := newDLAFixtureFromHarness(t, harness.New(t, harness.Options{
		ClockStart: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
	}))
	source := "banking:consistency-check"
	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             source,
		Date:            fixture.harness.Clock.Now(),
		Amount:          gbp(10_000),
		CashAccountCode: dlaCashAccount,
	})

	if err := fixture.harness.RunJob(dla.ConsistencyCheckJobName); err != nil {
		t.Fatalf("RunJob(%s) healthy error = %v", dla.ConsistencyCheckJobName, err)
	}
	assertDLAHealthStatus(t, fixture.harness, nethttp.StatusOK, "")

	raw := testdb.Raw(t)
	if _, err := raw.Exec(fixture.ctx, `
UPDATE dla.dla_entries
SET amount = amount + 1
WHERE source = $1`, source); err != nil {
		t.Fatalf("corrupt DLA entry via testdb.Raw: %v", err)
	}

	err := fixture.harness.RunJob(dla.ConsistencyCheckJobName)
	if !errors.Is(err, dla.ErrConsistencyViolation) {
		t.Fatalf("RunJob(%s) corrupt error = %v, want ErrConsistencyViolation", dla.ConsistencyCheckJobName, err)
	}
	if logText := logs.String(); !strings.Contains(logText, "invariant=violated") ||
		!strings.Contains(logText, dla.ConsistencyCheckJobName) {
		t.Fatalf("consistency violation log = %q, want invariant=violated and job name", logText)
	}
	assertDLAHealthStatus(t, fixture.harness, nethttp.StatusServiceUnavailable, "DLA balance mismatch")

	if _, err := raw.Exec(fixture.ctx, `
UPDATE dla.dla_entries
SET amount = amount - 1
WHERE source = $1`, source); err != nil {
		t.Fatalf("repair DLA entry via testdb.Raw: %v", err)
	}
	if err := fixture.harness.RunJob(dla.ConsistencyCheckJobName); err != nil {
		t.Fatalf("RunJob(%s) recovery error = %v", dla.ConsistencyCheckJobName, err)
	}
	assertDLAHealthStatus(t, fixture.harness, nethttp.StatusOK, "")
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAFutureDatedEntriesDoNotAffectCurrentFacts(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	fixture := newDLAFixtureFromHarness(t, harness.New(t, harness.Options{ClockStart: now}))

	var wentOverdrawn []dla.WentOverdrawn
	fixture.harness.Bus.Subscribe(dla.WentOverdrawnName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		wentOverdrawn = append(wentOverdrawn, evt.(dla.WentOverdrawn))
		return nil
	})
	var backInCredit []dla.BackInCredit
	fixture.harness.Bus.Subscribe(dla.BackInCreditName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		backInCredit = append(backInCredit, evt.(dla.BackInCredit))
		return nil
	})

	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:               now.AddDate(0, 0, 1),
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Scheduled expense owed",
		Amount:             gbp(1_000),
		Source:             "manual:future-credit",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(future expense owed) error = %v", err)
	}
	assertCurrentBalance(t, fixture.dla, gbp(0), dla.StatusCredit)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 0, 0)

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:current-drawing",
		Date:            now,
		Amount:          gbp(500),
		CashAccountCode: dlaCashAccount,
	})
	assertCurrentBalance(t, fixture.dla, gbp(-500), dla.StatusOverdrawn)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 1, 0)
	if wentOverdrawn[0].Balance != gbp(-500) {
		t.Fatalf("WentOverdrawn after future credit = %#v, want %#v", wentOverdrawn[0].Balance, gbp(-500))
	}

	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:            now.AddDate(0, 0, 1),
		Kind:            dla.EntryKindRepayment,
		Description:     "Scheduled director repayment",
		Amount:          gbp(500),
		Source:          "manual:future-repayment",
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("AddEntry(future repayment) error = %v", err)
	}
	assertCurrentBalance(t, fixture.dla, gbp(-500), dla.StatusOverdrawn)
	assertDLAEventCounts(t, wentOverdrawn, backInCredit, 1, 0)
	clearance, err := fixture.dla.SuggestedClearanceAmount(fixture.ctx, dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("SuggestedClearanceAmount() before future date error = %v", err)
	}
	if clearance != gbp(500) {
		t.Fatalf("SuggestedClearanceAmount() before future date = %#v, want %#v", clearance, gbp(500))
	}

	fixture.harness.Clock.Set(now.AddDate(0, 0, 1))
	assertCurrentBalance(t, fixture.dla, gbp(1_000), dla.StatusCredit)
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAConcurrentTransitionEventsAreSerialized(t *testing.T) {
	fixture := newDLAFixture(t)
	entryDate := fixture.harness.Clock.Now()
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:               entryDate,
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Seed credit balance",
		Amount:             gbp(100),
		Source:             "manual:seed-credit",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(seed credit) error = %v", err)
	}

	var (
		mu            sync.Mutex
		wentOverdrawn []dla.WentOverdrawn
		blockFirst    sync.Once
		firstEvent    = make(chan struct{})
		releaseFirst  = make(chan struct{})
	)
	fixture.harness.Bus.Subscribe(dla.WentOverdrawnName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		mu.Lock()
		wentOverdrawn = append(wentOverdrawn, evt.(dla.WentOverdrawn))
		mu.Unlock()

		blockFirst.Do(func() {
			close(firstEvent)
			<-releaseFirst
		})
		return nil
	})

	firstErr := make(chan error, 1)
	go func() {
		firstErr <- fixture.tryFileDrawingFromBanking(dla.TxnRef{
			Ref:             "banking:concurrent-crossing-1",
			Date:            entryDate,
			Amount:          gbp(150),
			CashAccountCode: dlaCashAccount,
		})
	}()

	select {
	case <-firstEvent:
	case <-time.After(5 * time.Second):
		t.Fatal("first concurrent crossing did not publish WentOverdrawn")
	}

	secondErr := make(chan error, 1)
	go func() {
		secondErr <- fixture.tryFileDrawingFromBanking(dla.TxnRef{
			Ref:             "banking:concurrent-crossing-2",
			Date:            entryDate,
			Amount:          gbp(150),
			CashAccountCode: dlaCashAccount,
		})
	}()

	select {
	case err := <-secondErr:
		t.Fatalf("second drawing completed before first transaction released; transition serialization missing, err=%v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstErr; err != nil {
		t.Fatalf("first concurrent FileDrawing error = %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second concurrent FileDrawing error = %v", err)
	}

	mu.Lock()
	gotEvents := append([]dla.WentOverdrawn(nil), wentOverdrawn...)
	mu.Unlock()
	if len(gotEvents) != 1 {
		t.Fatalf("concurrent WentOverdrawn count = %d (%#v), want 1", len(gotEvents), gotEvents)
	}
	if gotEvents[0].Balance != gbp(-50) {
		t.Fatalf("concurrent WentOverdrawn balance = %#v, want %#v", gotEvents[0].Balance, gbp(-50))
	}
	assertCurrentBalance(t, fixture.dla, gbp(-200), dla.StatusOverdrawn)
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLADuplicateSourceRejectedWithoutSecondLedgerPost(t *testing.T) {
	fixture := newDLAFixture(t)
	ref := "banking:txn-duplicate"

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             ref,
		Date:            time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(7_500),
		CashAccountCode: dlaCashAccount,
	})

	err := fixture.tryFileDrawingFromBanking(dla.TxnRef{
		Ref:             ref,
		Date:            time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(7_500),
		CashAccountCode: dlaCashAccount,
	})
	if !errors.Is(err, dla.ErrDuplicateSource) {
		t.Fatalf("FileDrawing(duplicate) error = %v, want ErrDuplicateSource", err)
	}
	var duplicate *dla.DuplicateSourceError
	if !errors.As(err, &duplicate) || duplicate.Source != ref {
		t.Fatalf("duplicate error = %#v, want source %q", err, ref)
	}
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "dla.dla_entries", "source = $1", 1, ref)
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "ledger.journal_entries", "source_module = 'dla' AND source_ref = $1", 1, ref)
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAFileDrawingUsesCallerTransaction(t *testing.T) {
	fixture := newDLAFixture(t)
	ref := "banking:txn-rollback"

	tx, err := fixture.banking.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("Begin() banking transaction error = %v", err)
	}
	if err := fixture.dla.FileDrawing(fixture.ctx, tx, dla.TxnRef{
		Ref:             ref,
		Date:            time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(5_500),
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("FileDrawing() inside rollback transaction error = %v", err)
	}
	if err := tx.Rollback(fixture.ctx); err != nil {
		t.Fatalf("Rollback() banking transaction error = %v", err)
	}

	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "dla.dla_entries", "source = $1", 0, ref)
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "ledger.journal_entries", "source_module = 'dla' AND source_ref = $1", 0, ref)
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLACompensatingEntryCorrectsMistakeWithoutUpdate(t *testing.T) {
	fixture := newDLAFixture(t)

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:mistaken-drawing",
		Date:            time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(10_000),
		CashAccountCode: dlaCashAccount,
	})
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:            time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Kind:            dla.EntryKindRepayment,
		Description:     "Correction for overstated drawing",
		Amount:          gbp(2_000),
		Source:          "manual:correction-mistaken-drawing",
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("AddEntry(correction) error = %v", err)
	}

	entries, err := fixture.dla.Ledger(fixture.ctx, dla.LedgerFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Ledger() error = %v", err)
	}
	assertDLAEntries(t, entries, []wantDLAEntry{
		{
			kind:           dla.EntryKindDrawing,
			source:         "banking:mistaken-drawing",
			amount:         10_000,
			drawn:          10_000,
			runningBalance: -10_000,
			side:           dla.BalanceSideDebit,
		},
		{
			kind:           dla.EntryKindRepayment,
			source:         "manual:correction-mistaken-drawing",
			amount:         2_000,
			owedToYou:      2_000,
			runningBalance: -8_000,
			side:           dla.BalanceSideDebit,
		},
	})
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "dla.dla_entries", "true", 2)

	_, err = fixture.dlaPool.Exec(fixture.ctx, `
UPDATE dla.dla_entries
SET amount = 8000
WHERE source = 'banking:mistaken-drawing'`)
	assertPermissionDenied(t, err)
	_, err = fixture.dlaPool.Exec(fixture.ctx, `
DELETE FROM dla.dla_entries
WHERE source = 'banking:mistaken-drawing'`)
	assertPermissionDenied(t, err)
	it.AssertLedgerBalanced(t, fixture.harness)
}

type dlaFixture struct {
	ctx     context.Context
	harness *harness.Harness
	dlaPool *pgxpool.Pool
	banking *pgxpool.Pool
	dla     *dla.Service
}

func newDLAFixture(t *testing.T) dlaFixture {
	t.Helper()
	return newDLAFixtureFromHarness(t, harness.New(t, harness.Options{}))
}

func newDLAFixtureFromHarness(t *testing.T, h *harness.Harness) dlaFixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	bankingPool := testdb.AsModule(t, "banking")
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	ensureLedgerAccount(t, ctx, h.LedgerPool, ledgerService, ledger.AccountSpec{
		Code:     dlaCashAccount,
		Name:     "DLA fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: stringPtr("GBP"),
	})

	return dlaFixture{
		ctx:     ctx,
		harness: h,
		dlaPool: h.DLAPool,
		banking: bankingPool,
		dla:     dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService),
	}
}

func (f dlaFixture) fileDrawingFromBanking(t *testing.T, src dla.TxnRef) {
	t.Helper()
	if err := f.tryFileDrawingFromBanking(src); err != nil {
		t.Fatalf("FileDrawing(%s) error = %v", src.Ref, err)
	}
}

func (f dlaFixture) tryFileDrawingFromBanking(src dla.TxnRef) (err error) {
	tx, err := f.banking.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = f.dla.FileDrawing(f.ctx, tx, src); err != nil {
		return err
	}
	if err = tx.Commit(f.ctx); err != nil {
		return err
	}
	return nil
}

func assertCurrentBalance(t *testing.T, service *dla.Service, wantBalance money.Money, wantStatus dla.Status) {
	t.Helper()

	gotBalance, gotStatus, err := service.CurrentBalance(context.Background(), dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("CurrentBalance() error = %v", err)
	}
	if gotBalance != wantBalance || gotStatus != wantStatus {
		t.Fatalf("CurrentBalance() = %#v/%s, want %#v/%s", gotBalance, gotStatus, wantBalance, wantStatus)
	}
	status, err := service.CurrentStatus(context.Background(), dla.DefaultDirectorID)
	if err != nil {
		t.Fatalf("CurrentStatus() error = %v", err)
	}
	if status.Balance != wantBalance || status.Status != wantStatus {
		t.Fatalf("CurrentStatus() = %#v/%s, want %#v/%s", status.Balance, status.Status, wantBalance, wantStatus)
	}
}

func assertDLAEventCounts(
	t *testing.T,
	wentOverdrawn []dla.WentOverdrawn,
	backInCredit []dla.BackInCredit,
	wantWent int,
	wantBack int,
) {
	t.Helper()

	if len(wentOverdrawn) != wantWent || len(backInCredit) != wantBack {
		t.Fatalf(
			"DLA event counts WentOverdrawn=%d BackInCredit=%d, want %d/%d",
			len(wentOverdrawn),
			len(backInCredit),
			wantWent,
			wantBack,
		)
	}
}

func loadDirectorLoanPolicyPack(t *testing.T, warn string, remedy string, s455 bool) {
	t.Helper()

	pack := fmt.Sprintf(`meta:
  id: testland
  version: "0.1"
  name: Testland
  currency: GBP
tax:
  year_end:
    month: 6
    day: 30
  corporate_income:
    "2025-26":
      standard_rate: "0.0"
  personal_income:
    "2025-26":
      personal_allowance_minor_units: 0
      bands:
        - rate: "0.10"
  dividends:
    "2025-26":
      withholding: none
      personal_tax_set_aside_template: 'set aside personally {{ estimate }}'
  vat:
    regime: test-shared
    authority: Testland Customs
    "2025-26":
      standard_rate: "0.20"
    treatments:
      domestic:
        output_vat: true
        vat_return_net_sales: true
      reverse-charge-eu-b2b:
        output_vat: false
        vat_return_net_sales: true
        reverse_charge_kind: b2b_services_eu
    reverse_charge:
      b2b_services_eu:
        article: Test Article 42
        invoice_wording: Testland reverse charge applies
filings:
  annual_return:
    due: incorporation_anniversary + 1 month
    authority: Testland Companies Office
  company_tax_return:
    due: accounting_year_end + 12 months + 1 day
    required_at_zero_rate: true
director_loans:
  s455_charge: %t
  credit:
    status_text: Testland credit wording
    explainer_template: Testland credit balance {{ balance }}
  overdrawn:
    warn: %s
    warning_template: Testland overdrawn balance {{ balance }}
    remedy: %s
advisor_rules:
  - id: test-rule
    severity: amber
    surfaces: [dashboard, reports]
    fact_query: [balance]
    condition: balance > 0
    text_template: Review the test balance before filing
    cta:
      label: Open test review
      action: test.openReview
`, s455, warn, remedy)

	if err := jurisdiction.LoadActiveFromFS(fstest.MapFS{
		"packs/testland/0.1/pack.yaml": &fstest.MapFile{Data: []byte(pack)},
	}, "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS(testland@0.1) error = %v", err)
	}
}

func assertDLAHealthStatus(t *testing.T, h *harness.Harness, wantStatus int, wantReason string) {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/healthz", nil)
	if err != nil {
		t.Fatalf("create GET /healthz request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET /healthz response: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET /healthz status = %d, want %d; body=%s", resp.StatusCode, wantStatus, string(bodyBytes))
	}
	if wantReason == "" {
		return
	}

	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode health response: %v; body=%s", err, string(bodyBytes))
	}
	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("health checks missing: %+v", body)
	}
	dlaCheck, ok := checks[dla.ConsistencyCheckJobName].(map[string]any)
	if !ok {
		t.Fatalf("%s health check missing: %+v", dla.ConsistencyCheckJobName, checks)
	}
	if dlaCheck["status"] != "down" {
		t.Fatalf("%s health check status = %v, want down", dla.ConsistencyCheckJobName, dlaCheck["status"])
	}
	reason, ok := dlaCheck["error"].(string)
	if !ok || !strings.Contains(reason, wantReason) {
		t.Fatalf("%s health error = %v, want text %q", dla.ConsistencyCheckJobName, dlaCheck["error"], wantReason)
	}
}

func (f dlaFixture) assertLedgerPostings(t *testing.T, source string, want []wantPosting) {
	t.Helper()

	rows, err := f.harness.DB.Query(f.ctx, `
SELECT p.account_code, p.amount, p.currency, p.amount_gbp
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = 'dla'
	AND je.source_ref = $1
ORDER BY p.id`, source)
	if err != nil {
		t.Fatalf("query ledger postings for %s: %v", source, err)
	}
	defer rows.Close()

	got := []wantPosting{}
	for rows.Next() {
		var posting wantPosting
		var currency string
		var amountGBP int64
		if err := rows.Scan(&posting.account, &posting.amount, &currency, &amountGBP); err != nil {
			t.Fatalf("scan ledger posting for %s: %v", source, err)
		}
		if currency != "GBP" || amountGBP != posting.amount {
			t.Fatalf("posting for %s has currency=%s amount_gbp=%d amount=%d, want GBP and matching GBP amount",
				source,
				currency,
				amountGBP,
				posting.amount,
			)
		}
		got = append(got, posting)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect ledger postings for %s: %v", source, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger postings for %s = %#v, want %#v", source, got, want)
	}
}

type wantPosting struct {
	account ledger.AccountCode
	amount  int64
}

type wantDLAEntry struct {
	kind           dla.EntryKind
	source         string
	amount         int64
	owedToYou      int64
	drawn          int64
	runningBalance int64
	side           dla.BalanceSide
}

func assertDLAEntries(t *testing.T, got []dla.Entry, want []wantDLAEntry) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("DLA entry count = %d (%#v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i].Kind != want[i].kind ||
			got[i].Source != want[i].source ||
			got[i].Amount != gbp(want[i].amount) ||
			got[i].OwedToYou != gbp(want[i].owedToYou) ||
			got[i].Drawn != gbp(want[i].drawn) ||
			got[i].RunningBalance != gbp(want[i].runningBalance) ||
			got[i].BalanceSide != want[i].side {
			t.Fatalf("DLA entry %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func ensureLedgerAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	service *ledger.Service,
	spec ledger.AccountSpec,
) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() ensure account error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	if _, err := service.EnsureAccount(ctx, tx, spec); err != nil {
		t.Fatalf("EnsureAccount(%s) error = %v", spec.Code, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() ensure account error = %v", err)
	}
}

func gbp(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "GBP"}
}

func stringPtr(value string) *string {
	return &value
}

func assertCountWhere(t *testing.T, ctx context.Context, tx db.Tx, table string, predicate string, want int, args ...any) {
	t.Helper()

	query := "SELECT count(*) FROM " + table + " WHERE " + predicate
	var got int
	if err := tx.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s where %s: %v", table, predicate, err)
	}
	if got != want {
		t.Fatalf("count %s where %s = %d, want %d", table, predicate, got, want)
	}
}

func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("app role mutation succeeded, want PostgreSQL insufficient_privilege")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("app role mutation error = %v, want PostgreSQL insufficient_privilege 42501", err)
	}
}
