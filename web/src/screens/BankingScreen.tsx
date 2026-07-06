import { AdvisorStrip, PageTitle } from "@/components";

export function BankingScreen() {
  return (
    <div className="banking-screen">
      <PageTitle>Banking</PageTitle>
      <AdvisorStrip surface="banking" />
    </div>
  );
}
