package fixtures

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

type RevolutTxn struct {
	Date      time.Time
	ID        string
	Type      string
	Payee     string
	Reference string
	Amount    money.Money
	Fee       money.Money
	State     string
	Balance   money.Money
}

// RevolutCSV builds a valid Revolut-format statement CSV for integration
// fixtures. Passing the same RevolutTxn more than once creates duplicates.
func RevolutCSV(txns ...RevolutTxn) []byte {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	mustWriteCSV(writer, []string{
		"Date started (UTC)",
		"Date completed (UTC)",
		"ID",
		"Type",
		"Description",
		"Reference",
		"Amount",
		"Fee",
		"Currency",
		"State",
		"Balance",
	})
	for i, txn := range txns {
		normalized := normalizeRevolutTxn(i, txn)
		mustWriteCSV(writer, []string{
			normalized.Date.Format("2006-01-02 15:04:05"),
			normalized.Date.Format("2006-01-02 15:04:05"),
			normalized.ID,
			normalized.Type,
			normalized.Payee,
			normalized.Reference,
			formatRevolutAmount(normalized.Amount),
			formatRevolutAmount(normalized.Fee),
			normalized.Amount.Currency,
			normalized.State,
			formatRevolutAmount(normalized.Balance),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		panic(fmt.Sprintf("fixtures: write Revolut CSV: %v", err))
	}
	return buf.Bytes()
}

func normalizeRevolutTxn(index int, txn RevolutTxn) RevolutTxn {
	if txn.Date.IsZero() {
		txn.Date = time.Date(2030, 1, 2+index, 12, 0, 0, 0, time.UTC)
	}
	if txn.ID == "" {
		txn.ID = fmt.Sprintf("revolut-fixture-%03d", index+1)
	}
	if txn.Type == "" {
		txn.Type = "CARD_PAYMENT"
	}
	if txn.Payee == "" {
		txn.Payee = fmt.Sprintf("Fixture payee %d", index+1)
	}
	if txn.Reference == "" {
		txn.Reference = txn.Payee
	}
	if txn.Amount.Currency == "" {
		txn.Amount = money.Money{Amount: int64(index+1) * 100, Currency: "GBP"}
	}
	if txn.Fee.Currency == "" {
		txn.Fee.Currency = txn.Amount.Currency
	}
	if txn.State == "" {
		txn.State = "COMPLETED"
	}
	if txn.Balance.Currency == "" {
		txn.Balance = money.Money{Amount: txn.Amount.Amount, Currency: txn.Amount.Currency}
	}
	return txn
}

func formatRevolutAmount(amount money.Money) string {
	sign := ""
	value := amount.Amount
	if value < 0 {
		sign = "-"
		value = -value
	}
	major := value / 100
	minor := value % 100
	return sign + groupDecimal(strconv.FormatInt(major, 10)) + "." + fmt.Sprintf("%02d", minor)
}

func groupDecimal(value string) string {
	if len(value) <= 3 {
		return value
	}
	var out []byte
	firstGroup := len(value) % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	out = append(out, value[:firstGroup]...)
	for i := firstGroup; i < len(value); i += 3 {
		out = append(out, ',')
		out = append(out, value[i:i+3]...)
	}
	return string(out)
}

func mustWriteCSV(writer *csv.Writer, record []string) {
	if err := writer.Write(record); err != nil {
		panic(fmt.Sprintf("fixtures: write Revolut CSV record: %v", err))
	}
}
