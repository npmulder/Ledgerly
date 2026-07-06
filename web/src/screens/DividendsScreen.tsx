import { useSearchParams } from "react-router-dom";

import { AdvisorStrip, Field, Input, PageTitle } from "@/components";

export function DividendsScreen() {
  const [params] = useSearchParams();
  const amount = params.get("amount") ?? "";

  return (
    <div className="dividends-screen">
      <PageTitle>Dividends</PageTitle>
      <AdvisorStrip surface="dividends" />
      <section className="dividends-prefill" aria-label="Dividend declaration">
        <Field label="Dividend amount">
          <Input
            defaultValue={amount}
            inputMode="decimal"
            key={amount}
            placeholder="0.00"
          />
        </Field>
      </section>
    </div>
  );
}
