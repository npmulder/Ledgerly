package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/npmulder/ledgerly/internal/cli/gen"
)

func newInvoiceCommand(runtime *Runtime) *cobra.Command {
	invoice := &cobra.Command{
		Use:   "invoice",
		Short: "Read invoices",
	}

	var listStatus string
	var listSearch string
	var listLimit int
	var listCursor string
	list := &cobra.Command{
		Use:   "list",
		Short: "List invoices",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInvoiceList(cmd.Context(), runtime, listStatus, listSearch, listLimit, listCursor)
		},
	}
	list.Flags().StringVar(&listStatus, "status", "", "filter by invoice status")
	list.Flags().StringVar(&listSearch, "search", "", "search invoice number or client")
	list.Flags().IntVar(&listLimit, "limit", 0, "page size")
	list.Flags().StringVar(&listCursor, "cursor", "", "next cursor")

	show := &cobra.Command{
		Use:   "show <number|id>",
		Short: "Show invoice details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvoiceShow(cmd.Context(), runtime, args[0])
		},
	}

	var pdfOutput string
	pdf := &cobra.Command{
		Use:   "pdf <id>",
		Short: "Download an invoice PDF",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvoicePDF(cmd.Context(), runtime, args[0], pdfOutput)
		},
	}
	pdf.Flags().StringVar(&pdfOutput, "output", "", "output PDF path")

	var createClient string
	var createLines []string
	create := &cobra.Command{
		Use:   "create --client <id> [--line \"desc:qty:price\"]...",
		Short: "Create a draft invoice",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInvoiceCreate(cmd.Context(), runtime, createClient, createLines)
		},
	}
	create.Flags().StringVar(&createClient, "client", "", "client id")
	create.Flags().StringArrayVar(&createLines, "line", nil, `invoice line in "desc:qty:price" form`)

	send := &cobra.Command{
		Use:   "send <id>",
		Short: "Send an invoice",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvoiceSend(cmd.Context(), runtime, args[0])
		},
	}

	remind := &cobra.Command{
		Use:   "remind <id>",
		Short: "Send an invoice reminder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvoiceRemind(cmd.Context(), runtime, args[0])
		},
	}

	revert := &cobra.Command{
		Use:   "revert <id>",
		Short: "Revert a sent invoice to draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvoiceRevert(cmd.Context(), runtime, args[0])
		},
	}

	invoice.AddCommand(list, show, pdf, create, send, remind, revert)
	return invoice
}

func newClientCommand(runtime *Runtime) *cobra.Command {
	client := &cobra.Command{
		Use:   "client",
		Short: "Read clients",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List clients",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runClientList(cmd.Context(), runtime)
		},
	}
	var addFlags clientAddFlags
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a client",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runClientAdd(cmd.Context(), runtime, addFlags)
		},
	}
	add.Flags().StringVar(&addFlags.fromJSON, "from-json", "", "read client JSON from path or -")
	add.Flags().StringVar(&addFlags.name, "name", "", "client name")
	add.Flags().StringVar(&addFlags.email, "email", "", "client email")
	add.Flags().StringVar(&addFlags.currency, "currency", "GBP", "default currency")
	add.Flags().IntVar(&addFlags.termsDays, "terms", 14, "payment terms in days")
	add.Flags().StringVar(&addFlags.vatTreatment, "vat-treatment", "domestic", "VAT treatment")
	add.Flags().StringVar(&addFlags.vatNumber, "vat-number", "", "VAT number")
	add.Flags().StringVar(&addFlags.retainerAmount, "retainer", "", "retainer amount")
	add.Flags().StringVar(&addFlags.dayRate, "day-rate", "", "day rate amount")
	add.Flags().StringVar(&addFlags.addressLine1, "address-line1", "", "address line 1")
	add.Flags().StringVar(&addFlags.addressLine2, "address-line2", "", "address line 2")
	add.Flags().StringVar(&addFlags.addressLocality, "locality", "", "address locality")
	add.Flags().StringVar(&addFlags.addressRegion, "region", "", "address region")
	add.Flags().StringVar(&addFlags.addressPostcode, "postal-code", "", "address postal code")
	add.Flags().StringVar(&addFlags.addressCountry, "country", "IM", "address country")

	client.AddCommand(list, add)
	return client
}

func newBankCommand(runtime *Runtime) *cobra.Command {
	bank := &cobra.Command{
		Use:   "bank",
		Short: "Read banking",
	}
	accounts := &cobra.Command{
		Use:   "accounts",
		Short: "List bank accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBankAccounts(cmd.Context(), runtime)
		},
	}
	review := &cobra.Command{
		Use:   "review",
		Short: "List banking review queue",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBankReview(cmd.Context(), runtime)
		},
	}

	var feedAccount int64
	var feedState string
	var feedCursor string
	feed := &cobra.Command{
		Use:   "feed",
		Short: "List banking feed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBankFeed(cmd.Context(), runtime, feedAccount, feedState, feedCursor)
		},
	}
	feed.Flags().Int64Var(&feedAccount, "account", 0, "bank account id")
	feed.Flags().StringVar(&feedState, "state", "", "transaction state")
	feed.Flags().StringVar(&feedCursor, "cursor", "", "next cursor")

	var importAccount int64
	importCSV := &cobra.Command{
		Use:   "import <file.csv> --account <id>",
		Short: "Import a bank statement CSV",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBankImport(cmd.Context(), runtime, args[0], importAccount)
		},
	}
	importCSV.Flags().Int64Var(&importAccount, "account", 0, "bank account id")

	confirm := &cobra.Command{
		Use:   "confirm <txn>",
		Short: "Confirm a suggested bank match",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			txnID, err := parsePositiveInt64(args[0], "txn")
			if err != nil {
				return err
			}
			return runBankConfirm(cmd.Context(), runtime, txnID)
		},
	}

	fileDLA := &cobra.Command{
		Use:   "file-dla <txn>",
		Short: "File a bank transaction to the director loan account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			txnID, err := parsePositiveInt64(args[0], "txn")
			if err != nil {
				return err
			}
			return runBankFileDLA(cmd.Context(), runtime, txnID)
		},
	}

	var recodeAccount string
	recode := &cobra.Command{
		Use:   "recode <txn> --account <code>",
		Short: "Recode a bank transaction to a ledger account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			txnID, err := parsePositiveInt64(args[0], "txn")
			if err != nil {
				return err
			}
			return runBankRecode(cmd.Context(), runtime, txnID, recodeAccount)
		},
	}
	recode.Flags().StringVar(&recodeAccount, "account", "", "target ledger account code")

	var excludeReason string
	exclude := &cobra.Command{
		Use:   "exclude <txn> --reason <reason>",
		Short: "Exclude a bank transaction from reconciliation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			txnID, err := parsePositiveInt64(args[0], "txn")
			if err != nil {
				return err
			}
			return runBankExclude(cmd.Context(), runtime, txnID, excludeReason)
		},
	}
	exclude.Flags().StringVar(&excludeReason, "reason", "", "exclusion reason")

	bank.AddCommand(accounts, review, feed, importCSV, confirm, fileDLA, recode, exclude)
	return bank
}

func newDLACommand(runtime *Runtime) *cobra.Command {
	dla := &cobra.Command{
		Use:   "dla",
		Short: "Read director loan account",
	}

	var ledgerFrom string
	var ledgerTo string
	var ledgerCursor string
	ledger := &cobra.Command{
		Use:   "ledger",
		Short: "List DLA ledger",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDLALedger(cmd.Context(), runtime, ledgerFrom, ledgerTo, ledgerCursor)
		},
	}
	ledger.Flags().StringVar(&ledgerFrom, "from", "", "inclusive entry date lower bound")
	ledger.Flags().StringVar(&ledgerTo, "to", "", "inclusive entry date upper bound")
	ledger.Flags().StringVar(&ledgerCursor, "cursor", "", "next cursor")

	balance := &cobra.Command{
		Use:   "balance",
		Short: "Show DLA balance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDLABalance(cmd.Context(), runtime)
		},
	}

	var addFlags dlaAddFlags
	add := &cobra.Command{
		Use:   "add --kind repayment|expense-owed --date YYYY-MM-DD --amount <amount> --description <text>",
		Short: "Add a manual DLA entry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDLAAdd(cmd.Context(), runtime, addFlags)
		},
	}
	add.Flags().StringVar(&addFlags.kind, "kind", "", "entry kind: repayment or expense-owed")
	add.Flags().StringVar(&addFlags.date, "date", "", "entry date YYYY-MM-DD")
	add.Flags().StringVar(&addFlags.description, "description", "", "entry description")
	add.Flags().StringVar(&addFlags.amount, "amount", "", "entry amount")
	add.Flags().StringVar(&addFlags.cashAccount, "cash-account", "", "cash/bank account code for repayments")
	add.Flags().StringVar(&addFlags.expenseCategory, "expense-category", "", "expense account code for expense-owed entries")
	add.Flags().StringVar(&addFlags.sourceRef, "source-ref", "", "manual source reference")

	dla.AddCommand(ledger, balance, add)
	return dla
}

func newDividendCommand(runtime *Runtime) *cobra.Command {
	dividend := &cobra.Command{
		Use:   "dividend",
		Short: "Read dividends",
	}
	headroom := &cobra.Command{
		Use:   "headroom",
		Short: "Show dividend headroom",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDividendHeadroom(cmd.Context(), runtime)
		},
	}
	history := &cobra.Command{
		Use:   "history",
		Short: "List dividend history",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDividendHistory(cmd.Context(), runtime)
		},
	}
	declare := &cobra.Command{
		Use:   "declare <amount>",
		Short: "Declare a dividend",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDividendDeclare(cmd.Context(), runtime, args[0])
		},
	}
	dividend.AddCommand(headroom, history, declare)
	return dividend
}

func newReportCommand(runtime *Runtime) *cobra.Command {
	report := &cobra.Command{
		Use:   "report",
		Short: "Read reports",
	}

	var plFrom string
	var plTo string
	pl := &cobra.Command{
		Use:   "pl",
		Short: "Show profit and loss",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReportPL(cmd.Context(), runtime, plFrom, plTo)
		},
	}
	pl.Flags().StringVar(&plFrom, "from", "", "inclusive posting date lower bound")
	pl.Flags().StringVar(&plTo, "to", "", "inclusive posting date upper bound")

	var vatPeriod string
	vat := &cobra.Command{
		Use:   "vat",
		Short: "Show VAT return",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReportVAT(cmd.Context(), runtime, vatPeriod)
		},
	}
	vat.Flags().StringVar(&vatPeriod, "period", "", "VAT quarter, for example 2026-Q2")

	calendar := &cobra.Command{
		Use:   "calendar",
		Short: "Show filing calendar",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReportCalendar(cmd.Context(), runtime)
		},
	}

	var taxYear string
	profitYTD := &cobra.Command{
		Use:   "profit-ytd",
		Short: "Show profit year to date",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReportProfitYTD(cmd.Context(), runtime, taxYear)
		},
	}
	profitYTD.Flags().StringVar(&taxYear, "tax-year", "", "tax year in YYYY-YY form")

	report.AddCommand(pl, vat, calendar, profitYTD)
	return report
}

func newAdvisorCommand(runtime *Runtime) *cobra.Command {
	advisor := &cobra.Command{
		Use:   "advisor",
		Short: "Read advisor insights",
	}
	var surface string
	insights := &cobra.Command{
		Use:   "insights",
		Short: "List advisor insights",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdvisorInsights(cmd.Context(), runtime, surface)
		},
	}
	insights.Flags().StringVar(&surface, "surface", "", "advisor surface")
	advisor.AddCommand(insights)
	return advisor
}

func newRatesCommand(runtime *Runtime) *cobra.Command {
	rates := &cobra.Command{
		Use:   "rates",
		Short: "Read FX rates",
	}
	var from string
	var to string
	today := &cobra.Command{
		Use:   "today",
		Short: "Show today's FX rate",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRatesToday(cmd.Context(), runtime, from, to)
		},
	}
	today.Flags().StringVar(&from, "from", "EUR", "source currency")
	today.Flags().StringVar(&to, "to", "GBP", "target currency")
	rates.AddCommand(today)
	return rates
}

func runInvoiceList(ctx context.Context, runtime *Runtime, status string, search string, limit int, cursor string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	params, err := invoiceListParams(status, search, limit, cursor)
	if err != nil {
		return err
	}
	response, err := client.client.InvoicingListInvoicesWithResponse(ctx, params)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderInvoiceList(runtime, response.JSON200)
}

func runInvoiceShow(ctx context.Context, runtime *Runtime, ref string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	invoice, err := getInvoiceByIDOrNumber(ctx, runtime, client, ref)
	if err != nil {
		return err
	}
	if runtime.json {
		return writeJSON(runtime.stdout, invoice)
	}
	return renderInvoiceShow(runtime, invoice)
}

func runInvoicePDF(ctx context.Context, runtime *Runtime, id string, output string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.InvoicingGetInvoicePDFWithResponse(ctx, id)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.StatusCode() >= 400 {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if response.StatusCode() < 200 || response.StatusCode() > 299 {
		return unexpectedAPIResponse(response.Status())
	}
	if len(response.Body) == 0 {
		return newDomainError("invoice PDF response was empty")
	}
	path := strings.TrimSpace(output)
	if path == "" {
		path = safePDFName(id)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return fmt.Errorf("create PDF output directory: %w", err)
	}
	if err := os.WriteFile(path, response.Body, 0o644); err != nil {
		return fmt.Errorf("write invoice PDF: %w", err)
	}
	result := map[string]any{
		"output":       path,
		"bytes":        len(response.Body),
		"content_type": response.HTTPResponse.Header.Get("Content-Type"),
	}
	if runtime.json {
		return writeJSON(runtime.stdout, result)
	}
	_, err = fmt.Fprintf(runtime.stdout, "Wrote %s (%d bytes)\n", path, len(response.Body))
	return err
}

func runClientList(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	includeArchived := false
	response, err := client.client.InvoicingListClientsWithResponse(ctx, &gen.InvoicingListClientsParams{IncludeArchived: &includeArchived})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderClientList(runtime, response.JSON200)
}

func runBankAccounts(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.BankingListAccountsWithResponse(ctx)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankAccounts(runtime, response.JSON200)
}

func runBankReview(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.BankingGetReviewQueueWithResponse(ctx)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankReview(runtime, response.JSON200)
}

func runBankFeed(ctx context.Context, runtime *Runtime, account int64, state string, cursor string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	params := &gen.BankingGetFeedParams{}
	if account > 0 {
		params.Account = &account
	}
	if strings.TrimSpace(state) != "" {
		value := gen.BankingGetFeedParamsState(strings.TrimSpace(state))
		params.State = &value
	}
	if strings.TrimSpace(cursor) != "" {
		params.Cursor = &cursor
	}
	response, err := client.client.BankingGetFeedWithResponse(ctx, params)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderBankFeed(runtime, response.JSON200)
}

func runDLALedger(ctx context.Context, runtime *Runtime, from string, to string, cursor string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	params := &gen.DlaListLedgerParams{}
	if strings.TrimSpace(from) != "" {
		parsed, err := parseAPIDate(from)
		if err != nil {
			return err
		}
		params.From = &parsed
	}
	if strings.TrimSpace(to) != "" {
		parsed, err := parseAPIDate(to)
		if err != nil {
			return err
		}
		params.To = &parsed
	}
	if strings.TrimSpace(cursor) != "" {
		params.Cursor = &cursor
	}
	response, err := client.client.DlaListLedgerWithResponse(ctx, params)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderDLALedger(runtime, response.JSON200)
}

func runDLABalance(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.DlaGetBalanceWithResponse(ctx, nil)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderDLABalance(runtime, response.JSON200)
}

func runDividendHeadroom(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.DividendsGetHeadroomWithResponse(ctx)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderDividendHeadroom(runtime, response.JSON200)
}

func runDividendHistory(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.DividendsGetHistoryWithResponse(ctx)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderDividendHistory(runtime, response.JSON200)
}

func runReportPL(ctx context.Context, runtime *Runtime, from string, to string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	period, err := reportPeriod(from, to)
	if err != nil {
		return err
	}
	response, err := client.client.ReportsGetProfitAndLossWithResponse(ctx, &gen.ReportsGetProfitAndLossParams{
		From: period.from,
		To:   period.to,
	})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderReportPL(runtime, response.JSON200)
}

func runReportVAT(ctx context.Context, runtime *Runtime, period string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	if strings.TrimSpace(period) == "" {
		period = currentVATPeriod(time.Now().UTC())
	}
	response, err := client.client.ReportsGetVATReturnWithResponse(ctx, &gen.ReportsGetVATReturnParams{Period: period})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderReportVAT(runtime, response.JSON200)
}

func runReportCalendar(ctx context.Context, runtime *Runtime) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	response, err := client.client.ReportsGetFilingCalendarWithResponse(ctx)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderReportCalendar(runtime, response.JSON200)
}

func runReportProfitYTD(ctx context.Context, runtime *Runtime, taxYear string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	if strings.TrimSpace(taxYear) == "" {
		taxYear = currentTaxYear(time.Now().UTC())
	}
	response, err := client.client.ReportsGetProfitYTDWithResponse(ctx, &gen.ReportsGetProfitYTDParams{TaxYear: taxYear})
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderReportProfitYTD(runtime, response.JSON200)
}

func runAdvisorInsights(ctx context.Context, runtime *Runtime, surface string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	params := &gen.AdvisorListInsightsParams{}
	if strings.TrimSpace(surface) != "" {
		value := gen.AdvisorListInsightsParamsSurface(strings.TrimSpace(surface))
		params.Surface = &value
	}
	response, err := client.client.AdvisorListInsightsWithResponse(ctx, params)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderAdvisorInsights(runtime, response.JSON200)
}

func runRatesToday(ctx context.Context, runtime *Runtime, from string, to string) error {
	client, err := newConfiguredAPIClient(runtime)
	if err != nil {
		return err
	}
	params := &gen.MoneyfxTodayRateParams{
		From: strings.ToUpper(strings.TrimSpace(from)),
		To:   strings.ToUpper(strings.TrimSpace(to)),
	}
	if params.From == "" || params.To == "" {
		return newUsageError("--from and --to are required")
	}
	response, err := client.client.MoneyfxTodayRateWithResponse(ctx, params)
	if err != nil {
		return newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 == nil {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404); err != nil {
			return err
		}
		return unexpectedAPIResponse(response.Status())
	}
	if runtime.json {
		return writeJSON(runtime.stdout, response.JSON200)
	}
	return renderRatesToday(runtime, response.JSON200)
}

func invoiceListParams(status string, search string, limit int, cursor string) (*gen.InvoicingListInvoicesParams, error) {
	params := &gen.InvoicingListInvoicesParams{}
	if strings.TrimSpace(status) != "" && strings.TrimSpace(status) != "all" {
		values := strings.Split(status, ",")
		statuses := make([]gen.InvoicingListInvoicesParamsStatus, 0, len(values))
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				statuses = append(statuses, gen.InvoicingListInvoicesParamsStatus(trimmed))
			}
		}
		params.Status = &statuses
	}
	if strings.TrimSpace(search) != "" {
		trimmed := strings.TrimSpace(search)
		params.Search = &trimmed
	}
	if limit > 0 {
		params.Limit = &limit
	}
	if strings.TrimSpace(cursor) != "" {
		offset, err := strconv.Atoi(strings.TrimSpace(cursor))
		if err != nil || offset < 0 {
			return nil, newUsageError("--cursor must be a non-negative invoice offset")
		}
		params.Offset = &offset
	}
	return params, nil
}

func getInvoiceByIDOrNumber(ctx context.Context, runtime *Runtime, client *apiClient, ref string) (*gen.InvoicingInvoice, error) {
	response, err := client.client.InvoicingGetInvoiceWithResponse(ctx, ref)
	if err != nil {
		return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 != nil {
		return response.JSON200, nil
	}
	if response.StatusCode() != 404 {
		if err := responseProblem(response.StatusCode(), response.Status(), response.Body, runtime.json, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404); err != nil {
			return nil, err
		}
		return nil, unexpectedAPIResponse(response.Status())
	}

	search := strings.TrimSpace(ref)
	limit := 10
	listResponse, err := client.client.InvoicingListInvoicesWithResponse(ctx, &gen.InvoicingListInvoicesParams{
		Search: &search,
		Limit:  &limit,
	})
	if err != nil {
		return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if listResponse.JSON200 == nil {
		if err := responseProblem(listResponse.StatusCode(), listResponse.Status(), listResponse.Body, runtime.json, listResponse.ApplicationproblemJSON400, listResponse.ApplicationproblemJSON401); err != nil {
			return nil, err
		}
		return nil, unexpectedAPIResponse(listResponse.Status())
	}
	for _, item := range listResponse.JSON200.Invoices {
		if strings.EqualFold(item.Id, ref) || (item.Number != nil && strings.EqualFold(*item.Number, ref)) {
			detailResponse, err := client.client.InvoicingGetInvoiceWithResponse(ctx, item.Id)
			if err != nil {
				return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
			}
			if detailResponse.JSON200 != nil {
				return detailResponse.JSON200, nil
			}
			if err := responseProblem(detailResponse.StatusCode(), detailResponse.Status(), detailResponse.Body, runtime.json, detailResponse.ApplicationproblemJSON401, detailResponse.ApplicationproblemJSON404); err != nil {
				return nil, err
			}
			return nil, unexpectedAPIResponse(detailResponse.Status())
		}
	}
	return nil, newDomainError(fmt.Sprintf("invoice %q was not found", ref))
}

func renderInvoiceList(runtime *Runtime, data *gen.InvoicingInvoicesResponse) error {
	if len(data.Invoices) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No invoices match the current filters.")
		return err
	}
	rows := make([][]string, 0, len(data.Invoices))
	for _, invoice := range data.Invoices {
		rows = append(rows, []string{
			valueOrDraft(invoice.Number),
			invoice.ClientName,
			formatDateTime(invoice.IssueDate),
			formatInvoicingMoney(invoice.Totals.Total),
			formatInvoiceLockedRate(invoice),
			formatInvoiceApproxGBP(invoice.Totals),
			string(invoice.Status),
		})
	}
	footers := []string{
		fmt.Sprintf("Showing %d of %d", len(data.Invoices), data.TotalCount),
		"Totals: " + formatInvoiceTotalsSummary(data.Totals),
	}
	if next := invoiceNextCursor(data); next != "" {
		footers = append(footers, "Next cursor: "+next)
	}
	return writeRowsTable(runtime.stdout, []string{"number", "client", "issued", "amount", "rate", "gbp approx", "status"}, rows, footers...)
}

func renderInvoiceShow(runtime *Runtime, invoice *gen.InvoicingInvoice) error {
	if err := writeTable(runtime.stdout, []tableRow{
		{Key: "id", Value: invoice.Id},
		{Key: "number", Value: valueOrDraft(invoice.Number)},
		{Key: "client id", Value: invoice.ClientId},
		{Key: "status", Value: string(invoice.Status)},
		{Key: "currency", Value: string(invoice.Currency)},
		{Key: "issue date", Value: formatDateTime(invoice.IssueDate)},
		{Key: "due date", Value: formatDateTime(invoice.DueDate)},
		{Key: "vat treatment", Value: string(invoice.VatTreatment)},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(runtime.stdout, ""); err != nil {
		return err
	}
	if len(invoice.Lines) == 0 {
		if _, err := fmt.Fprintln(runtime.stdout, "No invoice lines."); err != nil {
			return err
		}
	} else if err := writeRowsTable(runtime.stdout, []string{"description", "qty", "unit", "line total"}, invoiceLineRows(invoice.Lines)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(runtime.stdout, ""); err != nil {
		return err
	}
	if err := writeTable(runtime.stdout, []tableRow{
		{Key: "subtotal", Value: formatInvoicingMoney(invoice.Totals.Subtotal)},
		{Key: "vat", Value: formatInvoicingMoney(invoice.Totals.Vat)},
		{Key: "total", Value: formatInvoicingMoney(invoice.Totals.Total)},
		{Key: "gbp approx", Value: formatInvoiceApproxGBP(invoice.Totals)},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(runtime.stdout, ""); err != nil {
		return err
	}
	return writeTable(runtime.stdout, []tableRow{
		{Key: "settled date", Value: formatOptionalTime(invoice.SettledDate)},
		{Key: "settled amount", Value: formatOptionalInvoiceMoney(invoice.SettledAmount)},
		{Key: "settlement txn", Value: formatOptionalString(invoice.SettlementTxnRef)},
	})
}

func renderClientList(runtime *Runtime, data *gen.InvoicingClientsResponse) error {
	if len(data.Clients) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No clients found.")
		return err
	}
	rows := make([][]string, 0, len(data.Clients))
	for _, client := range data.Clients {
		rows = append(rows, []string{
			client.Id,
			client.Name,
			formatOptionalEmail(client.Email),
			string(client.DefaultCurrency),
			strconv.Itoa(int(client.TermsDays)),
			string(client.VatTreatment),
			formatOptionalMoneyAmount(client.RetainerAmount),
			formatOptionalMoneyAmount(client.DayRate),
		})
	}
	return writeRowsTable(runtime.stdout, []string{"id", "name", "email", "currency", "terms", "vat", "retainer", "day rate"}, rows)
}

func renderBankAccounts(runtime *Runtime, data *gen.BankingAccountsResponse) error {
	if len(data.Accounts) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No bank accounts found.")
		return err
	}
	rows := make([][]string, 0, len(data.Accounts))
	for _, account := range data.Accounts {
		rows = append(rows, []string{
			strconv.FormatInt(account.Id, 10),
			account.Name,
			string(account.Provider),
			account.Currency,
			account.LedgerAccountCode,
			strconv.Itoa(account.UnreconciledCount),
		})
	}
	return writeRowsTable(runtime.stdout, []string{"id", "name", "provider", "currency", "ledger", "unreconciled"}, rows)
}

func renderBankReview(runtime *Runtime, data *gen.BankingReviewQueue) error {
	cards := append(append([]gen.BankingReviewCard{}, data.Matches...), data.Suggestions...)
	cards = append(cards, data.Rules...)
	if len(cards) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No banking review cards waiting.")
		return err
	}
	rows := make([][]string, 0, len(cards))
	for _, card := range cards {
		rows = append(rows, []string{
			string(card.Kind),
			strconv.FormatInt(card.Transaction.Id, 10),
			formatAPIDate(card.Transaction.Date),
			card.Transaction.Payee,
			formatBankingMoney(card.Transaction.Amount),
			fmt.Sprintf("%.2f", card.Confidence),
			card.Explanation,
		})
	}
	return writeRowsTable(runtime.stdout, []string{"kind", "txn", "date", "payee", "amount", "confidence", "explanation"}, rows)
}

func renderBankFeed(runtime *Runtime, data *gen.BankingFeedResponse) error {
	if len(data.Transactions) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No bank transactions match the current filters.")
		return err
	}
	rows := make([][]string, 0, len(data.Transactions))
	for _, txn := range data.Transactions {
		rows = append(rows, []string{
			strconv.FormatInt(txn.Id, 10),
			strconv.FormatInt(txn.AccountId, 10),
			formatAPIDate(txn.Date),
			txn.Payee,
			txn.Reference,
			formatBankingMoney(txn.Amount),
			string(txn.State),
		})
	}
	footers := []string{}
	if data.NextCursor != nil && strings.TrimSpace(*data.NextCursor) != "" {
		footers = append(footers, "Next cursor: "+*data.NextCursor)
	}
	return writeRowsTable(runtime.stdout, []string{"id", "account", "date", "payee", "reference", "amount", "state"}, rows, footers...)
}

func renderDLALedger(runtime *Runtime, data *gen.DLALedgerResponse) error {
	if len(data.Entries) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No DLA ledger entries match the current filters.")
		return err
	}
	rows := make([][]string, 0, len(data.Entries))
	for _, entry := range data.Entries {
		rows = append(rows, []string{
			formatAPIDate(entry.Date),
			entry.Description,
			string(entry.Kind),
			formatDLAMoney(entry.OwedToYou),
			formatDLAMoney(entry.Drawn),
			formatDLAMoney(entry.RunningBalance) + " " + string(entry.BalanceSide),
		})
	}
	footers := []string{}
	if data.NextCursor != nil && strings.TrimSpace(*data.NextCursor) != "" {
		footers = append(footers, "Next cursor: "+*data.NextCursor)
	}
	return writeRowsTable(runtime.stdout, []string{"date", "entry", "kind", "owed to you", "drawn", "balance"}, rows, footers...)
}

func renderDLABalance(runtime *Runtime, data *gen.DLABalanceResponse) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "status", Value: string(data.Status)},
		{Key: "balance", Value: formatDLAMoney(data.Balance)},
		{Key: "suggested clearance", Value: formatOptionalDLAMoney(data.SuggestedClearance)},
		{Key: "policy status", Value: data.Policy.CreditStatusText},
		{Key: "policy remedy", Value: data.Policy.Remedy},
		{Key: "s455 charge", Value: strconv.FormatBool(data.Policy.S455Charge)},
	})
}

func renderDividendHeadroom(runtime *Runtime, data *gen.DividendsHeadroomBreakdown) error {
	if len(data.Lines) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No dividend headroom lines available.")
		return err
	}
	rows := make([][]string, 0, len(data.Lines))
	for _, line := range data.Lines {
		rows = append(rows, []string{line.Label, formatDividendMoney(line.Amount)})
	}
	return writeRowsTable(runtime.stdout, []string{"line", "amount"}, rows,
		"Financial year: "+data.FinancialYear,
		"As of: "+formatDateTime(data.AsOf),
		"Distributable: "+strconv.FormatBool(data.Distributable),
		"Available: "+formatDividendMoney(data.Available),
	)
}

func renderDividendHistory(runtime *Runtime, data *gen.DividendsHistoryResponse) error {
	if len(data.Declarations) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No dividend declarations found.")
		return err
	}
	rows := make([][]string, 0, len(data.Declarations))
	for _, declaration := range data.Declarations {
		rows = append(rows, []string{
			declaration.Id,
			formatDateTime(declaration.DeclaredDate),
			declaration.ShareholderName,
			formatDividendMoney(declaration.Amount),
			formatDividendMoney(declaration.PerShare),
			strconv.FormatInt(declaration.Shares, 10),
			documentState(declaration.VoucherAsset),
			documentState(declaration.MinutesAsset),
		})
	}
	return writeRowsTable(runtime.stdout, []string{"id", "declared", "shareholder", "amount", "per share", "shares", "voucher", "minutes"}, rows)
}

func renderReportPL(runtime *Runtime, data *gen.ReportsPLResponse) error {
	rows := [][]string{}
	for _, line := range data.Income {
		rows = append(rows, []string{"income", line.Label, formatReportsMoney(line.Amount)})
	}
	rows = append(rows, []string{"income", data.RealisedFxGains.Label, formatReportsMoney(data.RealisedFxGains.Amount)})
	rows = append(rows, []string{"total", "income total", formatReportsMoney(data.IncomeTotal)})
	for _, line := range data.Expenses {
		rows = append(rows, []string{"expense", line.AccountName, formatReportsMoney(line.Amount)})
	}
	rows = append(rows, []string{"total", "expense total", formatReportsMoney(data.ExpenseTotal)})
	rows = append(rows, []string{"total", "profit before tax", formatReportsMoney(data.ProfitBeforeTax)})
	rows = append(rows, []string{"tax", data.CorporateTax.Label, formatReportsMoney(data.CorporateTax.Amount)})
	rows = append(rows, []string{"total", "net profit", formatReportsMoney(data.NetProfit)})
	return writeRowsTable(runtime.stdout, []string{"kind", "line", "amount"}, rows,
		"Period: "+formatAPIDate(data.Period.From)+" to "+formatAPIDate(data.Period.To),
		"Tax year: "+data.TaxYear,
	)
}

func renderReportVAT(runtime *Runtime, data *gen.ReportsVATResponse) error {
	if data.Status == gen.NotRegistered {
		_, err := fmt.Fprintln(runtime.stdout, "Not VAT registered.")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(runtime.stdout, "Period: "+formatAPIDate(data.Period.From)+" to "+formatAPIDate(data.Period.To))
		return err
	}
	if data.Box1 == nil || data.Box4 == nil || data.Box6 == nil || data.NetPosition == nil {
		return fmt.Errorf("report vat response missing registered VAT boxes")
	}
	rows := [][]string{
		{"box1", "VAT due on sales", formatReportsMoney(*data.Box1)},
		{"box4", "VAT reclaimed", formatReportsMoney(*data.Box4)},
		{"box6", "Total sales ex-VAT", formatReportsMoney(*data.Box6)},
		{"net", "Net position", formatReportsMoney(*data.NetPosition)},
	}
	return writeRowsTable(runtime.stdout, []string{"box", "label", "amount"}, rows,
		"Period: "+formatAPIDate(data.Period.From)+" to "+formatAPIDate(data.Period.To),
	)
}

func renderReportCalendar(runtime *Runtime, data *gen.ReportsFilingCalendarResponse) error {
	if len(data.Filings) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No report filings found.")
		return err
	}
	rows := make([][]string, 0, len(data.Filings))
	for _, filing := range data.Filings {
		rows = append(rows, []string{
			filing.Key,
			filing.Label,
			filing.Authority,
			formatAPIDate(filing.DueDate),
			strconv.Itoa(int(filing.DaysUntil)),
			string(filing.Status),
		})
	}
	return writeRowsTable(runtime.stdout, []string{"key", "label", "authority", "due", "days", "status"}, rows)
}

func renderReportProfitYTD(runtime *Runtime, data *gen.ReportsProfitYTDResponse) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "tax year", Value: data.TaxYear},
		{Key: "profit", Value: formatReportsMoney(data.Profit)},
	})
}

func renderAdvisorInsights(runtime *Runtime, data *gen.AdvisorInsightsResponse) error {
	if len(data.Insights) == 0 {
		_, err := fmt.Fprintln(runtime.stdout, "No advisor insights for this surface.")
		return err
	}
	rows := make([][]string, 0, len(data.Insights))
	for _, insight := range data.Insights {
		rows = append(rows, []string{
			string(insight.Severity),
			insight.RenderedText,
			insight.Cta.Label,
			insight.Cta.Action,
		})
	}
	return writeRowsTable(runtime.stdout, []string{"severity", "text", "cta", "action"}, rows)
}

func renderRatesToday(runtime *Runtime, data *gen.MoneyFXRateResponse) error {
	return writeTable(runtime.stdout, []tableRow{
		{Key: "from", Value: data.From},
		{Key: "to", Value: data.To},
		{Key: "rate", Value: data.Rate},
		{Key: "rate date", Value: formatAPIDate(data.RateDate)},
		{Key: "fetched at", Value: formatDateTime(data.FetchedAt)},
		{Key: "source", Value: string(data.Source)},
	})
}

func invoiceLineRows(lines []gen.InvoicingInvoiceLine) [][]string {
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, []string{
			line.Description,
			line.Qty,
			formatInvoicingMoney(line.UnitPrice),
			formatInvoicingMoney(line.LineTotal),
		})
	}
	return rows
}

func invoiceNextCursor(data *gen.InvoicingInvoicesResponse) string {
	next := data.Offset + len(data.Invoices)
	if next >= data.TotalCount {
		return ""
	}
	return strconv.Itoa(next)
}

func formatInvoiceTotalsSummary(totals gen.InvoicingInvoiceTotalsSummary) string {
	parts := make([]string, 0, len(totals.Subtotals))
	for _, subtotal := range totals.Subtotals {
		parts = append(parts, formatInvoicingMoney(subtotal))
	}
	if len(parts) == 0 {
		parts = append(parts, "-")
	}
	return strings.Join(parts, " + ") + " approx " + formatInvoicingMoney(totals.TotalGbp)
}

func formatInvoiceLockedRate(invoice gen.InvoicingInvoiceListItem) string {
	if invoice.Totals.ApproxGbp == nil {
		return "-"
	}
	return invoice.Totals.ApproxGbp.Rate.Value
}

func formatInvoiceApproxGBP(totals gen.InvoicingInvoiceTotals) string {
	if totals.ApproxGbp == nil {
		return "-"
	}
	return formatInvoicingMoney(totals.ApproxGbp.Amount)
}

func formatInvoicingMoney(value gen.InvoicingMoney) string {
	return fmt.Sprintf("%d %s", value.Amount, value.Currency)
}

func formatOptionalInvoiceMoney(value *gen.InvoicingMoney) string {
	if value == nil {
		return "-"
	}
	return formatInvoicingMoney(*value)
}

func formatOptionalMoneyAmount(value *gen.InvoicingMoneyAmount) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d %s", value.AmountMinor, value.Currency)
}

func formatBankingMoney(value gen.BankingMoney) string {
	return fmt.Sprintf("%d %s", value.AmountMinor, value.Currency)
}

func formatDLAMoney(value gen.DLAMoney) string {
	return fmt.Sprintf("%d %s", value.AmountMinor, value.Currency)
}

func formatOptionalDLAMoney(value *gen.DLAMoney) string {
	if value == nil {
		return "-"
	}
	return formatDLAMoney(*value)
}

func formatDividendMoney(value gen.DividendsMoney) string {
	return fmt.Sprintf("%d %s", value.Amount, value.Currency)
}

func formatReportsMoney(value gen.ReportsMoney) string {
	return fmt.Sprintf("%d %s", value.AmountMinor, value.Currency)
}

func formatDateTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return formatDateTime(*value)
}

func formatAPIDate(value openapi_types.Date) string {
	if value.IsZero() {
		return "-"
	}
	return value.String()
}

func parseAPIDate(value string) (openapi_types.Date, error) {
	parsed, err := time.Parse(openapi_types.DateFormat, strings.TrimSpace(value))
	if err != nil {
		return openapi_types.Date{}, newUsageError(fmt.Sprintf("invalid date %q; expected YYYY-MM-DD", value))
	}
	return openapi_types.Date{Time: parsed}, nil
}

func reportPeriod(from string, to string) (struct {
	from openapi_types.Date
	to   openapi_types.Date
}, error) {
	if strings.TrimSpace(from) == "" && strings.TrimSpace(to) == "" {
		now := time.Now().UTC()
		start := time.Date(now.Year(), time.April, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(now.Year(), time.June, 30, 0, 0, 0, 0, time.UTC)
		return struct {
			from openapi_types.Date
			to   openapi_types.Date
		}{from: openapi_types.Date{Time: start}, to: openapi_types.Date{Time: end}}, nil
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return struct {
			from openapi_types.Date
			to   openapi_types.Date
		}{}, newUsageError("--from and --to must be provided together")
	}
	parsedFrom, err := parseAPIDate(from)
	if err != nil {
		return struct {
			from openapi_types.Date
			to   openapi_types.Date
		}{}, err
	}
	parsedTo, err := parseAPIDate(to)
	if err != nil {
		return struct {
			from openapi_types.Date
			to   openapi_types.Date
		}{}, err
	}
	return struct {
		from openapi_types.Date
		to   openapi_types.Date
	}{from: parsedFrom, to: parsedTo}, nil
}

func currentVATPeriod(now time.Time) string {
	quarter := int(now.Month()-1)/3 + 1
	return fmt.Sprintf("%04d-Q%d", now.Year(), quarter)
}

func currentTaxYear(now time.Time) string {
	start := now.Year()
	if now.Month() < time.April {
		start--
	}
	end := (start + 1) % 100
	return fmt.Sprintf("%04d-%02d", start, end)
}

func valueOrDraft(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "DRAFT"
	}
	return *value
}

func formatOptionalString(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}

func formatOptionalEmail(value *openapi_types.Email) string {
	if value == nil || strings.TrimSpace(string(*value)) == "" {
		return "-"
	}
	return string(*value)
}

func documentState(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "pending"
	}
	return "ready"
}

func safePDFName(id string) string {
	name := strings.TrimSpace(id)
	if name == "" {
		name = "invoice"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(name) + ".pdf"
}
