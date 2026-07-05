import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";

import { queryClient } from "@/api/queryClient";
import { App } from "@/app/App";
import { ApiErrorBannerProvider } from "@/app/ApiErrorBanner";
import "@/styles/tokens.css";
import "@/styles/base.css";
import "@/styles/global.css";

const rootElement = document.getElementById("root");

if (!rootElement) {
  throw new Error("Unable to find root element.");
}

createRoot(rootElement).render(
  <StrictMode>
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <ApiErrorBannerProvider>
          <App />
        </ApiErrorBannerProvider>
      </QueryClientProvider>
    </BrowserRouter>
  </StrictMode>,
);
