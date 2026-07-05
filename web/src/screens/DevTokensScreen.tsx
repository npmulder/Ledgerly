import type { CSSProperties } from "react";

type ColorToken = {
  readonly name: string;
  readonly cssVariable: string;
  readonly usage: string;
};

type TypeSample = {
  readonly name: string;
  readonly className: string;
  readonly sample: string;
  readonly detail: string;
};

const colorTokens: readonly ColorToken[] = [
  {
    name: "navy",
    cssVariable: "--color-navy",
    usage: "Primary buttons, active navigation, advisor panels",
  },
  {
    name: "teal",
    cssVariable: "--color-teal",
    usage: "Active underline and count badges",
  },
  {
    name: "teal-light",
    cssVariable: "--color-teal-light",
    usage: "Emphasis text on navy",
  },
  {
    name: "success",
    cssVariable: "--color-success",
    usage: "Success text and positive numerals",
  },
  {
    name: "success-bg",
    cssVariable: "--color-success-bg",
    usage: "Success badge background",
  },
  {
    name: "success-tint",
    cssVariable: "--color-success-tint",
    usage: "Success validation strips",
  },
  {
    name: "navy-tint",
    cssVariable: "--color-navy-tint",
    usage: "Paid badges and selected nav rows",
  },
  { name: "panel", cssVariable: "--color-panel", usage: "Secondary panels" },
  {
    name: "warning",
    cssVariable: "--color-warning",
    usage: "Deadline and warning text",
  },
  {
    name: "warning-bg",
    cssVariable: "--color-warning-bg",
    usage: "Warning badge background",
  },
  {
    name: "warning-callout",
    cssVariable: "--color-warning-callout",
    usage: "Warning callout background",
  },
  {
    name: "warning-border",
    cssVariable: "--color-warning-border",
    usage: "Warning callout border",
  },
  {
    name: "danger",
    cssVariable: "--color-danger",
    usage: "Overdue and error text",
  },
  {
    name: "danger-bg",
    cssVariable: "--color-danger-bg",
    usage: "Danger badge background",
  },
  {
    name: "danger-row-tint",
    cssVariable: "--color-danger-row-tint",
    usage: "Overdue row tint",
  },
  {
    name: "text",
    cssVariable: "--color-text",
    usage: "Primary interface text",
  },
  {
    name: "text-secondary",
    cssVariable: "--color-text-secondary",
    usage: "Secondary copy and table metadata",
  },
  {
    name: "text-muted",
    cssVariable: "--color-text-muted",
    usage: "Muted helper text",
  },
  {
    name: "page-bg",
    cssVariable: "--color-page-bg",
    usage: "Application page background",
  },
  {
    name: "card",
    cssVariable: "--color-card",
    usage: "Cards, header, document surfaces",
  },
  {
    name: "border-hairline",
    cssVariable: "--color-border-hairline",
    usage: "Card borders and separators",
  },
  {
    name: "border-input",
    cssVariable: "--color-border-input",
    usage: "Inputs and secondary buttons",
  },
  {
    name: "canvas",
    cssVariable: "--color-canvas",
    usage: "Outer prototype desk canvas",
  },
];

const typeSamples: readonly TypeSample[] = [
  {
    name: "Page title",
    className: "type-page-title",
    sample: "Dashboard",
    detail: "26px / 700",
  },
  {
    name: "Card title",
    className: "type-card-title",
    sample: "Recent invoices",
    detail: "15px / 700",
  },
  {
    name: "Body",
    className: "type-body",
    sample: "Contoso GmbH - July services",
    detail: "13.5px",
  },
  {
    name: "Secondary",
    className: "type-secondary",
    sample: "Due 15 Jul - locked rate 0.8534",
    detail: "12.5px",
  },
  {
    name: "Stat",
    className: "type-stat",
    sample: "£17,160.00",
    detail: "26px / 700",
  },
  {
    name: "Uppercase label",
    className: "type-uppercase-label",
    sample: "Outstanding",
    detail: "11.5px / 700 / 0.05em",
  },
  {
    name: "Mono numerals",
    className: "type-mono-numeral",
    sample: "INV-2026-07 - 0.8534 - £3,840.30",
    detail: "12.5px / 500",
  },
];

function getCssVariableValue(cssVariable: string) {
  if (typeof document === "undefined") {
    return cssVariable;
  }

  const computedValue = getComputedStyle(document.documentElement)
    .getPropertyValue(cssVariable)
    .trim();

  return computedValue || cssVariable;
}

function ColorSwatch({ token }: { readonly token: ColorToken }) {
  const tokenValue = getCssVariableValue(token.cssVariable);
  const swatchStyle: CSSProperties = {
    background: `var(${token.cssVariable})`,
  };

  return (
    <article className="token-swatch">
      <div className="token-swatch__preview" style={swatchStyle}>
        <span className="token-swatch__name">{token.name}</span>
      </div>
      <div className="token-swatch__body">
        <code className="type-mono-numeral token-swatch__value">
          {token.cssVariable} · {tokenValue}
        </code>
        <p className="type-secondary">{token.usage}</p>
      </div>
    </article>
  );
}

function TypeScaleRow({ sample }: { readonly sample: TypeSample }) {
  return (
    <article className="type-scale-row">
      <div className="type-scale-row__meta">
        <p className="type-card-title">{sample.name}</p>
        <p className="type-secondary">{sample.detail}</p>
      </div>
      <p className={sample.className}>{sample.sample}</p>
    </article>
  );
}

export function DevTokensScreen() {
  return (
    <main className="dev-token-page">
      <div className="dev-token-screen">
        <header className="dev-token-header">
          <div className="dev-token-header__meta">
            <p className="type-uppercase-label">Design tokens</p>
            <h1 className="type-page-title">Keel token source</h1>
          </div>
          <span className="dev-token-header__badge">/dev/tokens</span>
        </header>

        <div className="dev-token-content">
          <section
            className="dev-token-section"
            aria-labelledby="color-tokens-heading"
          >
            <div className="dev-token-section__header">
              <div>
                <p className="type-uppercase-label">Colors</p>
                <h2 className="type-card-title" id="color-tokens-heading">
                  Handoff swatches
                </h2>
              </div>
              <p className="type-secondary">
                Values are read from CSS variables at runtime.
              </p>
            </div>
            <div className="token-swatch-grid">
              {colorTokens.map((token) => (
                <ColorSwatch key={token.cssVariable} token={token} />
              ))}
            </div>
          </section>

          <section
            className="dev-token-section"
            aria-labelledby="type-scale-heading"
          >
            <div className="dev-token-section__header">
              <div>
                <p className="type-uppercase-label">Typography</p>
                <h2 className="type-card-title" id="type-scale-heading">
                  Type scale utilities
                </h2>
              </div>
              <p className="type-secondary">
                Instrument Sans for UI, IBM Plex Mono for numbers.
              </p>
            </div>
            <div className="type-scale-list">
              {typeSamples.map((sample) => (
                <TypeScaleRow key={sample.name} sample={sample} />
              ))}
            </div>
          </section>
        </div>
      </div>
    </main>
  );
}
