import {
  AmountText,
  Badge,
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  LockedField,
  Panel,
  Pill,
  Select,
  StatCard,
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeaderCell,
  TableRow,
} from "@/components";

const invoiceRows = [
  {
    amountMinor: 450000,
    client: "Contoso GmbH",
    currency: "EUR",
    gbp: "£3,840.30",
    issued: "01 Jul",
    number: "INV-2026-07",
    rate: "0.8534",
    status: "sent" as const,
  },
  {
    amountMinor: 120000,
    client: "Fabrikam Ltd",
    currency: "GBP",
    daysOverdue: 9,
    gbp: "£1,200.00",
    issued: "10 Jun",
    number: "INV-2026-F2",
    rate: "—",
    status: "overdue" as const,
    tone: "overdue" as const,
  },
  {
    amountMinor: 450000,
    client: "Contoso GmbH",
    currency: "EUR",
    gbp: "£3,812.85",
    issued: "01 Jun",
    number: "INV-2026-06",
    rate: "0.8473",
    status: "paid" as const,
  },
];

export function DevComponentsScreen() {
  return (
    <main className="dev-component-page">
      <div className="dev-component-screen">
        <header className="dev-component-header">
          <div className="dev-component-header__meta">
            <p className="type-uppercase-label">UI primitives</p>
            <h1 className="type-page-title">Keel component source</h1>
          </div>
          <span className="dev-component-header__badge">/dev/components</span>
        </header>

        <div className="dev-component-content">
          <section
            className="dev-component-section"
            aria-labelledby="cards-heading"
          >
            <div className="dev-component-section__header">
              <div>
                <p className="type-uppercase-label">Surfaces</p>
                <h2 className="type-card-title" id="cards-heading">
                  Card, panel, and stats
                </h2>
              </div>
            </div>
            <div className="dev-component-grid">
              <Card
                actions={
                  <Button size="small" variant="secondary">
                    View all
                  </Button>
                }
                title="Recent invoices"
              >
                <div className="dev-component-demo-card">
                  <p className="type-body">Contoso GmbH — July services</p>
                  <p className="type-secondary">
                    Locked rate 0.8534 · settlement gain tracked
                  </p>
                </div>
              </Card>
              <Panel
                eyebrow="Advisor · Isle of Man rules"
                title="Dividend headroom"
                variant="advisor"
              >
                <p className="dev-component-panel-note">
                  <strong>£17,160 available.</strong> IoM corporate tax is 0%,
                  so no CT provision is needed.
                </p>
                <p className="dev-component-panel-note">
                  <strong>Annual return due 14 Aug.</strong> File with Companies
                  Registry one month after anniversary.
                </p>
              </Panel>
              <StatCard
                label="Cash (GBP equiv.)"
                secondary="£18,240.55 GBP · €6,420.00 EUR"
                value={<AmountText amountMinor={2372068} currency="GBP" />}
              />
              <StatCard
                label="Outstanding"
                secondary="≈ £3,840.30 · due 15 Jul"
                value={<AmountText amountMinor={450000} currency="EUR" />}
              />
            </div>
          </section>

          <section
            className="dev-component-section"
            aria-labelledby="badges-heading"
          >
            <div className="dev-component-section__header">
              <div>
                <p className="type-uppercase-label">Status</p>
                <h2 className="type-card-title" id="badges-heading">
                  Badges and pills
                </h2>
              </div>
            </div>
            <Card>
              <div className="dev-component-row">
                <Badge variant="draft" />
                <Badge variant="sent" />
                <Badge variant="paid" />
                <Badge daysOverdue={9} variant="overdue" />
                <Badge variant="count">3</Badge>
                <Pill count={8} variant="active">
                  All
                </Pill>
                <Pill count={1}>Sent</Pill>
                <Pill count={1} variant="danger">
                  Overdue
                </Pill>
              </div>
            </Card>
          </section>

          <section
            className="dev-component-section"
            aria-labelledby="table-heading"
          >
            <div className="dev-component-section__header">
              <div>
                <p className="type-uppercase-label">Rows</p>
                <h2 className="type-card-title" id="table-heading">
                  Table with overdue tint and totals
                </h2>
              </div>
            </div>
            <Table aria-label="Invoice primitive demo">
              <TableHead>
                <TableRow>
                  <TableHeaderCell>Number</TableHeaderCell>
                  <TableHeaderCell>Client</TableHeaderCell>
                  <TableHeaderCell>Issued</TableHeaderCell>
                  <TableHeaderCell align="right">Amount</TableHeaderCell>
                  <TableHeaderCell align="right">Rate</TableHeaderCell>
                  <TableHeaderCell align="right">GBP</TableHeaderCell>
                  <TableHeaderCell align="right">Status</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {invoiceRows.map((invoice) => (
                  <TableRow key={invoice.number} tone={invoice.tone}>
                    <TableCell variant="mono">{invoice.number}</TableCell>
                    <TableCell>{invoice.client}</TableCell>
                    <TableCell>{invoice.issued}</TableCell>
                    <TableCell align="right" variant="numeric">
                      <AmountText
                        amountMinor={invoice.amountMinor}
                        currency={invoice.currency}
                      />
                    </TableCell>
                    <TableCell align="right" variant="mono-numeric">
                      {invoice.rate}
                    </TableCell>
                    <TableCell align="right">{invoice.gbp}</TableCell>
                    <TableCell align="right">
                      <Badge
                        daysOverdue={invoice.daysOverdue}
                        variant={invoice.status}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
              <TableFooter>
                <TableRow>
                  <TableCell colSpan={3}>
                    Showing 3 invoices · FY 2026-27
                  </TableCell>
                  <TableCell align="right" colSpan={4}>
                    Outstanding: €4,500.00 + £1,200.00 ≈ £5,040.30
                  </TableCell>
                </TableRow>
              </TableFooter>
            </Table>
          </section>

          <section
            className="dev-component-section"
            aria-labelledby="forms-heading"
          >
            <div className="dev-component-section__header">
              <div>
                <p className="type-uppercase-label">Actions</p>
                <h2 className="type-card-title" id="forms-heading">
                  Buttons and form controls
                </h2>
              </div>
            </div>
            <Card title="Invoice details">
              <div className="dev-component-demo-card">
                <div className="dev-component-row">
                  <Button>Raise July invoice</Button>
                  <Button variant="secondary">Export CSV</Button>
                  <Button variant="danger">Void invoice</Button>
                </div>
                <div className="dev-component-form-grid">
                  <Field label="Client">
                    <Input defaultValue="Contoso GmbH" />
                  </Field>
                  <Field label="Currency">
                    <Select defaultValue="EUR">
                      <option value="EUR">EUR — client default</option>
                      <option value="GBP">GBP</option>
                    </Select>
                  </Field>
                  <LockedField
                    label="FX rate at issue"
                    source="ECB 03 Jul"
                    value="0.8534"
                  />
                </div>
              </div>
            </Card>
          </section>

          <section
            className="dev-component-section"
            aria-labelledby="empty-heading"
          >
            <div className="dev-component-section__header">
              <div>
                <p className="type-uppercase-label">Fallbacks</p>
                <h2 className="type-card-title" id="empty-heading">
                  Empty state
                </h2>
              </div>
            </div>
            <EmptyState>
              All caught up — every transaction to 02 Jul is coded. Next
              statement import suggested Friday.
            </EmptyState>
          </section>
        </div>
      </div>
    </main>
  );
}
