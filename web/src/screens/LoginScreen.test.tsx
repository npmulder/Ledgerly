import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { LoginScreen } from "@/screens/LoginScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("LoginScreen", () => {
  it("links first-time users to registration", async () => {
    const user = userEvent.setup();

    renderLogin();

    expect(screen.getByText("First time here?")).toBeInTheDocument();
    const registerLink = screen.getByRole("link", { name: "Set up Ledgerly" });
    expect(registerLink).toHaveAttribute("href", "/register");

    await user.click(registerLink);

    expect(screen.getByTestId("location")).toHaveTextContent("/register");
  });

  it("renders problem details from failed login responses", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(
          {
            detail: "authentication required",
            status: 401,
            title: "Unauthorized",
            type: "https://ledgerly.local/problems/unauthenticated",
          },
          401,
          "application/problem+json",
        ),
      ),
    );

    renderLogin();

    await user.type(screen.getByLabelText("Email"), "owner@example.com");
    await user.type(screen.getByLabelText("Password"), "wrong password");
    await user.click(screen.getByRole("button", { name: "Login" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Unauthorized");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "authentication required",
    );
  });

  it("returns to the URL query target after login", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse({
          created_at: "2026-07-05T12:00:00Z",
          email: "owner@example.com",
          id: 1,
          name: "N. Meyer",
        }),
      ),
    );

    renderLogin(
      `/login?returnTo=${encodeURIComponent("/settings/company?source=api")}`,
    );

    await user.type(screen.getByLabelText("Email"), "owner@example.com");
    await user.type(screen.getByLabelText("Password"), "correct password");
    await user.click(screen.getByRole("button", { name: "Login" }));

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/settings/company?source=api",
      );
    });
  });
});

function renderLogin(initialEntry = "/login") {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={queryClient}>
        <LoginScreen />
        <LocationProbe />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function LocationProbe() {
  const location = useLocation();
  return (
    <span data-testid="location">
      {location.pathname}
      {location.search}
      {location.hash}
    </span>
  );
}

function jsonResponse(
  body: unknown,
  status = 200,
  contentType = "application/json",
) {
  return new Response(JSON.stringify(body), {
    headers: {
      "Content-Type": contentType,
    },
    status,
  });
}
