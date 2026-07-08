package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/npmulder/ledgerly/internal/cli/gen"
)

var cliDecimalQuantityPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func runInvoiceCreate(ctx context.Context, runtime *Runtime, clientID string, rawLines []string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return newUsageError("--client is required")
	}
	lines, err := parseInvoiceLineInputs(rawLines, "")
	if err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	if len(lines) > 0 {
		clientResponse, err := client.client.InvoicingGetClientWithResponse(ctx, clientID)
		if err != nil {
			return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
		}
		if clientResponse.JSON200 == nil {
			if err := responseProblem(clientResponse.StatusCode(), clientResponse.Status(), clientResponse.Body, runtime.json, clientResponse.ApplicationproblemJSON401, clientResponse.ApplicationproblemJSON404); err != nil {
				return err
			}
			return unexpectedAPIResponse(clientResponse.Status())
		}
		invoiceCurrency := string(clientResponse.JSON200.DefaultCurrency)
		for i := range lines {
			lineCurrency := strings.TrimSpace(string(lines[i].UnitPrice.Currency))
			if lineCurrency == "" {
				lines[i].UnitPrice.Currency = gen.InvoicingMoneyCurrency(invoiceCurrency)
				continue
			}
			if !strings.EqualFold(lineCurrency, invoiceCurrency) {
				return newUsageError(fmt.Sprintf("--line %d price currency must match client currency %s", i+1, invoiceCurrency))
			}
		}
	}
	response, err := client.client.InvoicingCreateDraftInvoiceWithResponse(ctx, gen.InvoicingCreateDraftInvoiceRequest{
		ClientId: clientID,
	})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON201 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, response.ApplicationproblemJSON413, problemFromValidation(response.ApplicationproblemJSON422)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}

	invoice := response.JSON201
	if len(lines) > 0 {
		patchResponse, err := client.client.InvoicingPatchInvoiceWithResponse(ctx, invoice.Id, gen.InvoicingInvoicePatch{
			Lines: &lines,
		})
		if err != nil {
			return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
		}
		if patchResponse.JSON200 == nil {
			if err := responseProblem(patchResponse.StatusCode(), patchResponse.Status(), patchResponse.Body, runtime.json, patchResponse.ApplicationproblemJSON400, patchResponse.ApplicationproblemJSON401, patchResponse.ApplicationproblemJSON404, problemFromValidation(patchResponse.ApplicationproblemJSON409), patchResponse.ApplicationproblemJSON413, problemFromValidation(patchResponse.ApplicationproblemJSON422)); err != nil {
				return err
			}
			return unexpectedAPIResponse(patchResponse.Status())
		}
		invoice = patchResponse.JSON200
	}

	if runtime.json {
		return writeJSON(runtime.stdout, invoice)
	}
	return renderInvoiceMutation(runtime, "created", invoice)
}

func runInvoiceSend(ctx context.Context, runtime *Runtime, id string) error {
	if err := confirmAction(runtime, "send invoice "+strings.TrimSpace(id), func(w io.Writer) error {
		client, err := newConfiguredAPIClient(runtime)
		if err != nil {
			return err
		}
		invoice, err := getInvoiceByIDOrNumber(ctx, runtime, client, id)
		if err != nil {
			return err
		}
		return renderInvoicePreview(w, invoice, "sent")
	}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.InvoicingSendInvoiceWithResponse(ctx, strings.TrimSpace(id))
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, problemFromValidation(response.ApplicationproblemJSON409), problemFromValidation(response.ApplicationproblemJSON422)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderInvoiceSend(runtime, response.JSON200)
}

func runInvoiceRemind(ctx context.Context, runtime *Runtime, id string) error {
	if err := confirmAction(runtime, "send invoice reminder "+strings.TrimSpace(id), func(w io.Writer) error {
		client, err := newConfiguredAPIClient(runtime)
		if err != nil {
			return err
		}
		invoice, err := getInvoiceByIDOrNumber(ctx, runtime, client, id)
		if err != nil {
			return err
		}
		return renderInvoicePreview(w, invoice, string(invoice.Status))
	}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.InvoicingSendInvoiceReminderWithResponse(ctx, strings.TrimSpace(id))
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, problemFromValidation(response.ApplicationproblemJSON409)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderInvoiceReminder(runtime, response.JSON200)
}

func runInvoiceRevert(ctx context.Context, runtime *Runtime, id string) error {
	if err := confirmAction(runtime, "revert invoice "+strings.TrimSpace(id), func(w io.Writer) error {
		client, err := newConfiguredAPIClient(runtime)
		if err != nil {
			return err
		}
		invoice, err := getInvoiceByIDOrNumber(ctx, runtime, client, id)
		if err != nil {
			return err
		}
		return renderInvoicePreview(w, invoice, "draft")
	}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.InvoicingRevertInvoiceWithResponse(ctx, strings.TrimSpace(id))
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, problemFromValidation(response.ApplicationproblemJSON409)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderInvoiceMutation(runtime, "reverted", response.JSON200)
}

func runClientAdd(ctx context.Context, runtime *Runtime, flags clientAddFlags) error {
	request, err := clientAddRequest(runtime, flags)
	if err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.InvoicingCreateClientWithResponse(ctx, request)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON201 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON413, problemFromValidation(response.ApplicationproblemJSON422)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON201)
	}
	return renderClientCreated(runtime, response.JSON201)
}

func runBankImport(ctx context.Context, runtime *Runtime, path string, accountID int64) error {
	if accountID <= 0 {
		return newUsageError("--account is required")
	}
	if strings.TrimSpace(path) == "" {
		return newUsageError("CSV file path is required")
	}
	if err := confirmAction(runtime, "import bank statement "+filepath.Base(path), func(w io.Writer) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		return writeTable(w, []tableRow{
			{Key: "file", Value: path},
			{Key: "account", Value: strconv.FormatInt(accountID, 10)},
			{Key: "size bytes", Value: strconv.FormatInt(info.Size(), 10)},
			{Key: "resulting state", Value: "statement rows imported for review"},
		})
	}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	body, contentType, err := multipartCSV(path)
	if err != nil {
		return err
	}
	response, err := client.client.BankingImportAccountCSVWithBodyWithResponse(ctx, accountID, contentType, body)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, response.ApplicationproblemJSON413, response.ApplicationproblemJSON415, problemFromBankingValidation(response.ApplicationproblemJSON422)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankBatchSummary(runtime, response.JSON200)
}

func runBankConfirm(ctx context.Context, runtime *Runtime, txnID int64) error {
	if err := confirmBankTransaction(ctx, runtime, txnID, "confirm bank transaction", "reconciled", nil); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.BankingConfirmTransactionWithBodyWithResponse(ctx, txnID, "application/json", nil)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, response.ApplicationproblemJSON409, response.ApplicationproblemJSON413, response.ApplicationproblemJSON422); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankCommand(runtime, "confirmed", response.JSON200)
}

func runBankFileDLA(ctx context.Context, runtime *Runtime, txnID int64) error {
	if err := confirmBankTransaction(ctx, runtime, txnID, "file bank transaction to DLA", "reconciled + DLA drawing", nil); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.BankingFileTransactionToDLAWithResponse(ctx, txnID)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, response.ApplicationproblemJSON409, response.ApplicationproblemJSON413, response.ApplicationproblemJSON422); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankCommand(runtime, "filed to DLA", response.JSON200)
}

func runBankRecode(ctx context.Context, runtime *Runtime, txnID int64, accountCode string) error {
	accountCode = strings.TrimSpace(accountCode)
	if accountCode == "" {
		return newUsageError("--account is required")
	}
	if err := confirmBankTransaction(ctx, runtime, txnID, "recode bank transaction", "reconciled + ledger recode", []tableRow{{Key: "target account", Value: accountCode}}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.BankingRecodeTransactionWithResponse(ctx, txnID, gen.BankingRecodeRequest{AccountCode: accountCode})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, response.ApplicationproblemJSON409, response.ApplicationproblemJSON413, response.ApplicationproblemJSON422); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankCommand(runtime, "recoded", response.JSON200)
}

func runBankExclude(ctx context.Context, runtime *Runtime, txnID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return newUsageError("--reason is required")
	}
	if err := confirmBankTransaction(ctx, runtime, txnID, "exclude bank transaction", "excluded", []tableRow{{Key: "reason", Value: reason}}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.BankingExcludeTransactionWithResponse(ctx, txnID, gen.BankingReasonRequest{Reason: reason})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, response.ApplicationproblemJSON409, response.ApplicationproblemJSON413, response.ApplicationproblemJSON422); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankCommand(runtime, "excluded", response.JSON200)
}

func runDLAAdd(ctx context.Context, runtime *Runtime, flags dlaAddFlags) error {
	if strings.TrimSpace(flags.kind) == "" {
		return newUsageError("--kind is required")
	}
	if strings.TrimSpace(flags.date) == "" {
		return newUsageError("--date is required")
	}
	if strings.TrimSpace(flags.description) == "" {
		return newUsageError("--description is required")
	}
	amount, currency, err := parseMoney(flags.amount, "GBP", "--amount")
	if err != nil {
		return err
	}
	date, err := parseAPIDate(flags.date)
	if err != nil {
		return err
	}
	var cashAccount *string
	if strings.TrimSpace(flags.cashAccount) != "" {
		value := strings.TrimSpace(flags.cashAccount)
		cashAccount = &value
	}
	var expenseCategory *string
	if strings.TrimSpace(flags.expenseCategory) != "" {
		value := strings.TrimSpace(flags.expenseCategory)
		expenseCategory = &value
	}
	var sourceRef *string
	if strings.TrimSpace(flags.sourceRef) != "" {
		value := strings.TrimSpace(flags.sourceRef)
		sourceRef = &value
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.DlaCreateEntryWithResponse(ctx, gen.DLAEntryRequest{
		Amount: gen.DLAMoney{
			AmountMinor: amount,
			Currency:    currency,
		},
		CashAccountCode: cashAccount,
		Date:            date,
		Description:     strings.TrimSpace(flags.description),
		ExpenseCategory: expenseCategory,
		Kind:            gen.DLAEntryRequestKind(strings.TrimSpace(flags.kind)),
		SourceRef:       sourceRef,
	})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON201 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON409, response.ApplicationproblemJSON413, problemFromDLAValidation(response.ApplicationproblemJSON422)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON201)
	}
	return renderDLAEntryCreated(runtime, response.JSON201)
}

func runDividendDeclare(ctx context.Context, runtime *Runtime, rawAmount string) error {
	amount, currency, err := parseMoney(rawAmount, "GBP", "amount")
	if err != nil {
		return err
	}
	request := gen.DividendsAmountRequest{
		Amount: gen.DividendsMoney{Amount: amount, Currency: currency},
	}
	if err := confirmAction(runtime, "declare dividend "+formatDividendMoney(request.Amount), func(w io.Writer) error {
		client, err := newConfiguredAPIClient(runtime)
		if err != nil {
			return err
		}
		response, err := client.client.DividendsValidateAmountWithResponse(ctx, request)
		if err != nil {
			return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
		}
		if response.JSON200 == nil {
			if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON413, problemFromDividendsValidation(response.ApplicationproblemJSON422)); err != nil {
				return err
			}
			return unexpectedAPIResponse(response.Status())
		}
		return renderDividendValidationPreview(w, response.JSON200)
	}); err != nil {
		return err
	}
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.DividendsDeclareAmountWithResponse(ctx, request)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON201 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON413, problemFromDividendsValidation(response.ApplicationproblemJSON422)); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON201)
	}
	return renderDividendDeclaration(runtime, response.JSON201)
}

type clientAddFlags struct {
	fromJSON        string
	name            string
	email           string
	currency        string
	termsDays       int
	vatTreatment    string
	vatNumber       string
	retainerAmount  string
	dayRate         string
	addressLine1    string
	addressLine2    string
	addressLocality string
	addressRegion   string
	addressPostcode string
	addressCountry  string
}

type dlaAddFlags struct {
	kind            string
	date            string
	description     string
	amount          string
	cashAccount     string
	expenseCategory string
	sourceRef       string
}

func clientAddRequest(runtime *Runtime, flags clientAddFlags) (gen.InvoicingClientRequest, error) {
	if strings.TrimSpace(flags.fromJSON) != "" {
		var data []byte
		var err error
		if strings.TrimSpace(flags.fromJSON) == "-" {
			data, err = io.ReadAll(runtime.stdin)
		} else {
			data, err = os.ReadFile(flags.fromJSON)
		}
		if err != nil {
			return gen.InvoicingClientRequest{}, err
		}
		var request gen.InvoicingClientRequest
		if err := json.Unmarshal(data, &request); err != nil {
			return gen.InvoicingClientRequest{}, newUsageError(fmt.Sprintf("decode --from-json: %v", err))
		}
		return request, nil
	}
	if strings.TrimSpace(flags.name) == "" {
		return gen.InvoicingClientRequest{}, newUsageError("--name is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(flags.currency))
	if currency == "" {
		currency = "GBP"
	}
	terms := flags.termsDays
	if terms == 0 {
		terms = 14
	}
	vatTreatment := strings.TrimSpace(flags.vatTreatment)
	if vatTreatment == "" {
		vatTreatment = "domestic"
	}
	var email *openapi_types.Email
	if strings.TrimSpace(flags.email) != "" {
		value := openapi_types.Email(strings.TrimSpace(flags.email))
		email = &value
	}
	var vatNumber *string
	if strings.TrimSpace(flags.vatNumber) != "" {
		value := strings.TrimSpace(flags.vatNumber)
		vatNumber = &value
	}
	retainer, err := optionalMoneyAmount(flags.retainerAmount, currency, "--retainer")
	if err != nil {
		return gen.InvoicingClientRequest{}, err
	}
	dayRate, err := optionalMoneyAmount(flags.dayRate, currency, "--day-rate")
	if err != nil {
		return gen.InvoicingClientRequest{}, err
	}
	return gen.InvoicingClientRequest{
		Name:            strings.TrimSpace(flags.name),
		Email:           email,
		DefaultCurrency: gen.InvoicingClientRequestDefaultCurrency(currency),
		TermsDays:       gen.InvoicingClientRequestTermsDays(terms),
		VatTreatment:    gen.InvoicingClientRequestVatTreatment(vatTreatment),
		VatNumber:       vatNumber,
		RetainerAmount:  retainer,
		DayRate:         dayRate,
		Address: gen.InvoicingAddress{
			Line1:      strings.TrimSpace(flags.addressLine1),
			Line2:      strings.TrimSpace(flags.addressLine2),
			Locality:   strings.TrimSpace(flags.addressLocality),
			Region:     strings.TrimSpace(flags.addressRegion),
			PostalCode: strings.TrimSpace(flags.addressPostcode),
			Country:    strings.TrimSpace(flags.addressCountry),
		},
	}, nil
}

func optionalMoneyAmount(raw string, defaultCurrency string, label string) (*gen.InvoicingMoneyAmount, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	amount, currency, err := parseMoney(raw, defaultCurrency, label)
	if err != nil {
		return nil, err
	}
	return &gen.InvoicingMoneyAmount{
		AmountMinor: amount,
		Currency:    gen.InvoicingMoneyAmountCurrency(currency),
	}, nil
}

func parseInvoiceLineInputs(rawLines []string, defaultCurrency string) ([]gen.InvoicingInvoiceLineInput, error) {
	lines := make([]gen.InvoicingInvoiceLineInput, 0, len(rawLines))
	for i, raw := range rawLines {
		parts := strings.Split(raw, ":")
		if len(parts) != 3 {
			return nil, newUsageError(`--line must use "desc:qty:price"`)
		}
		description := strings.TrimSpace(parts[0])
		if description == "" {
			return nil, newUsageError(fmt.Sprintf("--line %d description is required", i+1))
		}
		qty := strings.TrimSpace(parts[1])
		if err := validateDecimalQuantity(qty); err != nil {
			return nil, newUsageError(fmt.Sprintf("invalid --line %d qty %q: %v", i+1, parts[1], err))
		}
		amount, currency, err := parseInvoiceLinePrice(parts[2], defaultCurrency)
		if err != nil {
			return nil, err
		}
		if amount <= 0 {
			return nil, newUsageError(fmt.Sprintf("--line %d price must be greater than zero", i+1))
		}
		lines = append(lines, gen.InvoicingInvoiceLineInput{
			Description: description,
			Qty:         qty,
			UnitPrice: gen.InvoicingMoney{
				Amount:   amount,
				Currency: gen.InvoicingMoneyCurrency(currency),
			},
		})
	}
	return lines, nil
}

func parseInvoiceLinePrice(raw string, defaultCurrency string) (int64, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, "", newUsageError("--line price is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(defaultCurrency))
	fields := strings.Fields(value)
	switch len(fields) {
	case 1:
		amountPart, detectedCurrency := splitCurrencySuffix(fields[0])
		value = amountPart
		if detectedCurrency != "" {
			currency = detectedCurrency
		}
	case 2:
		value = fields[0]
		currency = strings.ToUpper(strings.TrimSpace(fields[1]))
	default:
		return 0, "", newUsageError("--line price must be an amount optionally followed by a currency")
	}
	if currency != "" && currency != "EUR" && currency != "GBP" {
		return 0, "", newUsageError("--line price currency must be EUR or GBP")
	}
	amount, err := parseMinorAmount(value)
	if err != nil {
		return 0, "", newUsageError(fmt.Sprintf("invalid --line price %q: %v", raw, err))
	}
	return amount, currency, nil
}

func validateDecimalQuantity(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("is required")
	}
	if !cliDecimalQuantityPattern.MatchString(value) {
		return fmt.Errorf("must be a positive decimal")
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || amount <= 0 {
		return fmt.Errorf("must be a positive decimal")
	}
	return nil
}

func parseMoney(raw string, defaultCurrency string, label string) (int64, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, "", newUsageError(label + " is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(defaultCurrency))
	if currency == "" {
		currency = "GBP"
	}
	fields := strings.Fields(value)
	switch len(fields) {
	case 1:
		amountPart, detectedCurrency := splitCurrencySuffix(fields[0])
		value = amountPart
		if detectedCurrency != "" {
			currency = detectedCurrency
		}
	case 2:
		value = fields[0]
		currency = strings.ToUpper(strings.TrimSpace(fields[1]))
	default:
		return 0, "", newUsageError(label + " must be an amount optionally followed by a currency")
	}
	amount, err := parseMinorAmount(value)
	if err != nil {
		return 0, "", newUsageError(fmt.Sprintf("invalid %s %q: %v", label, raw, err))
	}
	return amount, currency, nil
}

func splitCurrencySuffix(value string) (string, string) {
	if len(value) < 4 {
		return value, ""
	}
	suffix := value[len(value)-3:]
	for _, r := range suffix {
		if !unicode.IsLetter(r) {
			return value, ""
		}
	}
	prefix := strings.TrimSpace(value[:len(value)-3])
	if prefix == "" {
		return value, ""
	}
	return prefix, strings.ToUpper(suffix)
}

func parseMinorAmount(value string) (int64, error) {
	clean := strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	if clean == "" {
		return 0, fmt.Errorf("amount is required")
	}
	negative := strings.HasPrefix(clean, "-")
	if negative || strings.HasPrefix(clean, "+") {
		clean = clean[1:]
	}
	if strings.Contains(clean, ".") {
		parts := strings.Split(clean, ".")
		if len(parts) != 2 || parts[0] == "" || len(parts[1]) > 2 {
			return 0, fmt.Errorf("expected at most two decimal places")
		}
		fraction := parts[1]
		for len(fraction) < 2 {
			fraction += "0"
		}
		whole, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, err
		}
		minor, err := strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, err
		}
		amount := whole*100 + minor
		if negative {
			amount = -amount
		}
		return amount, nil
	}
	amount, err := strconv.ParseInt(clean, 10, 64)
	if err != nil {
		return 0, err
	}
	if negative {
		amount = -amount
	}
	return amount, nil
}

func parsePositiveInt64(raw string, label string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0, newUsageError(label + " must be a positive integer")
	}
	return value, nil
}

func multipartCSV(path string) (io.Reader, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_ = file.Close()
	}()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return &body, writer.FormDataContentType(), nil
}

func confirmBankTransaction(ctx context.Context, runtime *Runtime, txnID int64, action string, resultingState string, extra []tableRow) error {
	return confirmAction(runtime, fmt.Sprintf("%s %d", action, txnID), func(w io.Writer) error {
		client, err := newConfiguredAPIClient(runtime)
		if err != nil {
			return err
		}
		rows := []tableRow{
			{Key: "transaction", Value: strconv.FormatInt(txnID, 10)},
			{Key: "resulting state", Value: resultingState},
		}
		txn, err := findBankTransaction(ctx, runtime, client, txnID)
		if err != nil {
			return err
		}
		if txn != nil {
			rows = append([]tableRow{
				{Key: "transaction", Value: strconv.FormatInt(txn.Id, 10)},
				{Key: "counterparty", Value: txn.Payee},
				{Key: "reference", Value: txn.Reference},
				{Key: "amount", Value: formatBankingMoney(txn.Amount)},
				{Key: "current state", Value: string(txn.State)},
				{Key: "resulting state", Value: resultingState},
			}, extra...)
		} else {
			rows = append(rows, extra...)
		}
		return writeTable(w, rows)
	})
}

func findBankTransaction(ctx context.Context, runtime *Runtime, client *apiClient, txnID int64) (*gen.BankingTransaction, error) {
	response, err := client.client.BankingGetFeedWithResponse(ctx, &gen.BankingGetFeedParams{})
	if err != nil {
		return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return nil, err
		}
		return nil, unexpectedAPIResponse(response.Status())
	}
	for _, txn := range response.JSON200.Transactions {
		if txn.Id == txnID {
			return &txn, nil
		}
	}

	limit := 100
	recent, err := client.client.BankingGetRecentWithResponse(ctx, &gen.BankingGetRecentParams{Limit: &limit})
	if err != nil {
		return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if recent.JSON200 == nil {
		if err := responseProblem(recent.StatusCode(), recent.Status(), recent.Body, runtime.json, recent.ApplicationproblemJSON400, recent.ApplicationproblemJSON401); err != nil {
			return nil, err
		}
		return nil, unexpectedAPIResponse(recent.Status())
	}
	for _, item := range recent.JSON200.Transactions {
		if item.Transaction.Id == txnID {
			txn := item.Transaction
			return &txn, nil
		}
	}
	return nil, nil
}

func renderInvoicePreview(w io.Writer, invoice *gen.InvoicingInvoice, resultingState string) error {
	number := valueOrDraft(invoice.Number)
	return writeTable(w, []tableRow{
		{Key: "invoice", Value: number},
		{Key: "id", Value: invoice.Id},
		{Key: "counterparty", Value: invoice.ClientId},
		{Key: "amount", Value: formatInvoicingMoney(invoice.Totals.Total)},
		{Key: "current state", Value: string(invoice.Status)},
		{Key: "resulting state", Value: resultingState},
	})
}

func renderInvoiceMutation(runtime *Runtime, action string, invoice *gen.InvoicingInvoice) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "invoice", Value: valueOrDraft(invoice.Number)},
		{Key: "id", Value: invoice.Id},
		{Key: "action", Value: action},
		{Key: "client id", Value: invoice.ClientId},
		{Key: "status", Value: string(invoice.Status)},
		{Key: "amount", Value: formatInvoicingMoney(invoice.Totals.Total)},
	})
}

func renderInvoiceSend(runtime *Runtime, result *gen.InvoicingSendInvoiceResult) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "invoice", Value: result.Number},
		{Key: "id", Value: result.Invoice.Id},
		{Key: "status", Value: string(result.Invoice.Status)},
		{Key: "amount", Value: formatInvoicingMoney(result.Invoice.Totals.Total)},
		{Key: "locked rate", Value: result.LockedRate.Rate},
	})
}

func renderInvoiceReminder(runtime *Runtime, result *gen.InvoicingReminderResult) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "invoice", Value: valueOrDraft(result.Invoice.Number)},
		{Key: "id", Value: result.Invoice.Id},
		{Key: "status", Value: string(result.Invoice.Status)},
		{Key: "reminder sent", Value: formatDateTime(result.Reminder.SentAt)},
	})
}

func renderClientCreated(runtime *Runtime, client *gen.InvoicingClient) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "id", Value: client.Id},
		{Key: "name", Value: client.Name},
		{Key: "currency", Value: string(client.DefaultCurrency)},
		{Key: "terms", Value: strconv.Itoa(int(client.TermsDays))},
		{Key: "vat treatment", Value: string(client.VatTreatment)},
	})
}

func renderBankBatchSummary(runtime *Runtime, summary *gen.BankingBatchSummary) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "batch", Value: strconv.FormatInt(summary.BatchId, 10)},
		{Key: "account", Value: strconv.FormatInt(summary.AccountId, 10)},
		{Key: "file", Value: summary.Filename},
		{Key: "total", Value: strconv.Itoa(summary.Total)},
		{Key: "new", Value: strconv.Itoa(summary.New)},
		{Key: "duplicates", Value: strconv.Itoa(summary.Duplicates)},
		{Key: "imported at", Value: formatDateTime(summary.ImportedAt)},
	})
}

func renderBankCommand(runtime *Runtime, action string, result *gen.BankingCommandResponse) error {
	rows := []tableRow{{Key: "action", Value: action}}
	if result.Transaction != nil {
		rows = append(rows,
			tableRow{Key: "transaction", Value: strconv.FormatInt(result.Transaction.Id, 10)},
			tableRow{Key: "counterparty", Value: result.Transaction.Payee},
			tableRow{Key: "amount", Value: formatBankingMoney(result.Transaction.Amount)},
			tableRow{Key: "state", Value: string(result.Transaction.State)},
		)
	}
	if result.StateChange != nil {
		rows = append(rows, tableRow{Key: "state change", Value: fmt.Sprintf("%s -> %s", result.StateChange.From, result.StateChange.To)})
	}
	if result.AmountGbp != nil {
		rows = append(rows, tableRow{Key: "amount GBP", Value: formatBankingMoney(*result.AmountGbp)})
	}
	if result.RealisedFxAmount != nil {
		rows = append(rows, tableRow{Key: "realised FX", Value: formatBankingMoney(*result.RealisedFxAmount)})
	}
	if result.Rule != nil {
		rows = append(rows, tableRow{Key: "rule account", Value: result.Rule.AccountCode})
	}
	return writeTable(runtime.stdout, rows)
}

func renderDLAEntryCreated(runtime *Runtime, result *gen.DLAEntryCreatedResponse) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "source ref", Value: result.SourceRef},
	})
}

func renderDividendValidationPreview(w io.Writer, result *gen.DividendsValidationResult) error {
	return writeTable(w, []tableRow{
		{Key: "amount", Value: formatDividendMoney(result.Amount)},
		{Key: "headroom check", Value: fmt.Sprintf("within_headroom=%t distributable=%t", result.WithinHeadroom, result.Distributable)},
		{Key: "available headroom", Value: formatDividendMoney(result.Headroom.Available)},
		{Key: "set-aside estimate", Value: formatDividendMoney(result.PersonalTax.Marginal)},
		{Key: "set-aside message", Value: result.PersonalTax.Message},
		{Key: "withholding", Value: fmt.Sprintf("applies=%t policy=%s", result.Withholding.Applies, result.Withholding.Policy)},
	})
}

func renderDividendDeclaration(runtime *Runtime, declaration *gen.DividendsDeclaration) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "id", Value: declaration.Id},
		{Key: "declared", Value: formatDateTime(declaration.DeclaredDate)},
		{Key: "shareholder", Value: declaration.ShareholderName},
		{Key: "amount", Value: formatDividendMoney(declaration.Amount)},
		{Key: "per share", Value: formatDividendMoney(declaration.PerShare)},
		{Key: "shares", Value: strconv.FormatInt(declaration.Shares, 10)},
	})
}

func problemFromValidation(problem *gen.ValidationProblem) *gen.Problem {
	if problem == nil {
		return nil
	}
	return &gen.Problem{Type: problem.Type, Title: problem.Title, Status: problem.Status, Detail: problem.Detail, Instance: problem.Instance}
}

func problemFromBankingValidation(problem *gen.BankingValidationProblem) *gen.Problem {
	if problem == nil {
		return nil
	}
	return &gen.Problem{Type: problem.Type, Title: problem.Title, Status: problem.Status, Detail: problem.Detail, Instance: problem.Instance}
}

func problemFromDLAValidation(problem *gen.DLAValidationProblem) *gen.Problem {
	if problem == nil {
		return nil
	}
	return &gen.Problem{Type: problem.Type, Title: problem.Title, Status: problem.Status, Detail: problem.Detail, Instance: problem.Instance}
}

func problemFromDividendsValidation(problem *gen.DividendsValidationProblem) *gen.Problem {
	if problem == nil {
		return nil
	}
	return &gen.Problem{Type: problem.Type, Title: problem.Title, Status: problem.Status, Detail: problem.Detail, Instance: problem.Instance}
}
