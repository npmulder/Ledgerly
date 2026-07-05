import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { LoginScreen } from "@/screens/LoginScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("LoginScreen", () => {
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
});

function renderLogin() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter initialEntries={["/login"]}>
      <QueryClientProvider client={queryClient}>
        <LoginScreen />
      </QueryClientProvider>
    </MemoryRouter>,
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
