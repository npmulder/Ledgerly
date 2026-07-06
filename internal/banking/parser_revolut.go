package banking

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func init() {
	registerDefaultParser(ProviderRevolut, RevolutParser{})
}

// RevolutParser parses Revolut account statement CSV exports.
type RevolutParser struct{}

type revolutHeader struct {
	date      int
	amount    int
	fee       int
	currency  int
	payee     int
	reference int
	state     int
}

var (
	revolutDateColumns = [][]string{
		{"date completed (utc)", "completed date", "completed at", "date"},
		{"date started (utc)", "started date", "started at"},
	}
	revolutAmountColumns    = []string{"amount"}
	revolutFeeColumns       = []string{"fee"}
	revolutCurrencyColumns  = []string{"currency", "payment currency"}
	revolutPayeeColumns     = []string{"description", "payee", "counterparty", "name"}
	revolutReferenceColumns = []string{"reference", "description", "id"}
	revolutStateColumns     = []string{"state"}
	revolutDateLayouts      = []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"02/01/2006 15:04:05",
		"02/01/2006 15:04",
		"02/01/2006",
	}
)

func (RevolutParser) Parse(r io.Reader) ([]RawTxn, error) {
	if r == nil {
		return nil, fmt.Errorf("banking: Revolut CSV reader is required: %w", ErrInvalidImport)
	}
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("banking: Revolut CSV header is required: %w", ErrInvalidImport)
		}
		return nil, fmt.Errorf("banking: read Revolut CSV header: %w", err)
	}
	columns, err := parseRevolutHeader(header)
	if err != nil {
		return nil, err
	}

	var txns []RawTxn
	row := 1
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		row++
		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) && parseErr.Line > 0 {
				row = parseErr.Line
			}
			return nil, &ParseRowError{Row: row, Err: err}
		}
		if blankCSVRecord(record) {
			continue
		}
		if len(record) != len(header) {
			return nil, &ParseRowError{
				Row: row,
				Err: fmt.Errorf("got %d fields, want %d: %w", len(record), len(header), ErrInvalidImport),
			}
		}
		txn, include, err := parseRevolutRecord(header, columns, record)
		if err != nil {
			return nil, &ParseRowError{Row: row, Err: err}
		}
		if !include {
			continue
		}
		txns = append(txns, txn)
	}
	return txns, nil
}

func parseRevolutHeader(header []string) (revolutHeader, error) {
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[normalizeRevolutHeader(name)] = i
	}
	dateColumn := -1
	for _, group := range revolutDateColumns {
		dateColumn = revolutColumn(index, group)
		if dateColumn >= 0 {
			break
		}
	}
	columns := revolutHeader{
		date:      dateColumn,
		amount:    revolutColumn(index, revolutAmountColumns),
		fee:       revolutColumn(index, revolutFeeColumns),
		currency:  revolutColumn(index, revolutCurrencyColumns),
		payee:     revolutColumn(index, revolutPayeeColumns),
		reference: revolutColumn(index, revolutReferenceColumns),
		state:     revolutColumn(index, revolutStateColumns),
	}
	missing := []string{}
	if columns.date < 0 {
		missing = append(missing, "date")
	}
	if columns.amount < 0 {
		missing = append(missing, "amount")
	}
	if columns.currency < 0 {
		missing = append(missing, "currency")
	}
	if columns.payee < 0 {
		missing = append(missing, "payee")
	}
	if len(missing) > 0 {
		return revolutHeader{}, fmt.Errorf("banking: Revolut CSV missing %s column(s): %w", strings.Join(missing, ", "), ErrInvalidImport)
	}
	return columns, nil
}

func parseRevolutRecord(header []string, columns revolutHeader, record []string) (RawTxn, bool, error) {
	if columns.state >= 0 && !revolutStateCompleted(record[columns.state]) {
		return RawTxn{}, false, nil
	}
	date, err := parseRevolutDate(record[columns.date])
	if err != nil {
		return RawTxn{}, false, err
	}
	currency := strings.ToUpper(strings.TrimSpace(record[columns.currency]))
	amount, err := money.ParseAmount(record[columns.amount], currency)
	if err != nil {
		return RawTxn{}, false, err
	}
	if columns.fee >= 0 && strings.TrimSpace(record[columns.fee]) != "" {
		fee, err := money.ParseAmount(record[columns.fee], currency)
		if err != nil {
			return RawTxn{}, false, err
		}
		amount, err = amount.Add(fee)
		if err != nil {
			return RawTxn{}, false, err
		}
	}
	payee := strings.TrimSpace(record[columns.payee])
	if payee == "" {
		return RawTxn{}, false, fmt.Errorf("payee is required: %w", ErrInvalidImport)
	}
	reference := payee
	if columns.reference >= 0 {
		if value := strings.TrimSpace(record[columns.reference]); value != "" {
			reference = value
		}
	}
	providerMeta := make(map[string]string, len(header))
	for i, name := range header {
		providerMeta[strings.TrimSpace(name)] = record[i]
	}
	return RawTxn{
		Date:         date,
		Amount:       amount,
		Payee:        payee,
		Reference:    reference,
		ProviderMeta: providerMeta,
	}, true, nil
}

func revolutColumn(index map[string]int, aliases []string) int {
	for _, alias := range aliases {
		if column, ok := index[normalizeRevolutHeader(alias)]; ok {
			return column
		}
	}
	return -1
}

func normalizeRevolutHeader(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "\ufeff"))), " ")
}

func parseRevolutDate(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("date is required: %w", ErrInvalidImport)
	}
	for _, layout := range revolutDateLayouts {
		parsed, err := time.ParseInLocation(layout, trimmed, time.UTC)
		if err == nil {
			year, month, day := parsed.Date()
			return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("date %q is not a supported Revolut date: %w", value, ErrInvalidImport)
}

func revolutStateCompleted(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "completed")
}

func blankCSVRecord(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}
