import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RegisterScreen } from "@/screens/RegisterScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("RegisterScreen", () => {
  it("validates the owner account step before advancing", async () => {
    const user = userEvent.setup();

    renderRegister();

    await user.type(screen.getByLabelText("Email"), "not-an-email");
    await user.type(screen.getByLabelText("Name"), "N. Meyer");
    await user.type(screen.getByLabelText("Password"), "short");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(screen.getByText("Use a valid email address.")).toBeInTheDocument();
    expect(
      screen.getByText("Use at least 10 characters with letters and numbers."),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Trading name")).not.toBeInTheDocument();
  });

  it("posts the wizard payload and navigates to the dashboard", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        void init;
        if (pathFromRequest(input) === "/api/identity/register-with-profile") {
          return jsonResponse(registerResult(), 201);
        }

        return problemResponse("Not found", 404);
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderRegister();

    await fillOwnerStep(user);
    await fillCompanyStep(user);
    await user.click(screen.getByRole("button", { name: "Create profile" }));

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/");
    });

    const registerCall = fetchImpl.mock.calls.find(
      ([input]) =>
        pathFromRequest(input) === "/api/identity/register-with-profile",
    );
    expect(registerCall).toBeDefined();
    if (!registerCall) {
      throw new Error("missing register call");
    }

    const [, init] = registerCall;
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toMatchObject({
      company_number: "137792C",
      directors: [
        {
          appointed_date: "2025-04-03",
          is_chair: true,
          name: "N. Meyer",
        },
      ],
      email: "owner@example.com",
      incorporation_date: "2025-04-03",
      legal_name: "NPM Limited",
      name: "N. Meyer",
      registered_office: {
        country: "IM",
        line1: "12 Quay Street",
        line2: "",
        locality: "Douglas",
        postal_code: "IM1 1AA",
        region: "",
      },
      trading_name: "Ledgerly Consulting",
      year_end_day: 31,
      year_end_month: 3,
    });
  });

  it("explains closed registration and links back to login", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        problemResponse(
          "Registration is closed",
          403,
          "A company profile already exists.",
        ),
      ),
    );

    renderRegister();

    await fillOwnerStep(user);
    await fillCompanyStep(user);
    await user.click(screen.getByRole("button", { name: "Create profile" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Registration is closed");
    expect(alert).toHaveTextContent("A company profile already exists.");
    expect(
      screen.getByRole("link", { name: "Return to login" }),
    ).toHaveAttribute("href", "/login");
  });

  it("renders 400 field errors inline", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        problemResponse("Invalid first-run registration request", 400, "", [
          { detail: "company number already exists", pointer: "/company_number" },
          {
            detail: "registered office line 1 is required",
            pointer: "/registered_office/line1",
          },
          {
            detail: "year-end day 31 out of range for month 2",
            pointer: "/year_end_day",
          },
        ]),
      ),
    );

    renderRegister();

    await fillOwnerStep(user);
    await fillCompanyStep(user);
    await user.click(screen.getByRole("button", { name: "Create profile" }));

    expect(
      await screen.findByText("company number already exists"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("registered office line 1 is required"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("year-end day 31 out of range for month 2"),
    ).toBeInTheDocument();
  });

  it.each(["3.5", "1e2"])(
    "rejects non-integer year-end day input %s before submitting",
    async (yearEndDay) => {
      const user = userEvent.setup();
      const fetchImpl = vi.fn();
      vi.stubGlobal("fetch", fetchImpl);

      renderRegister();

      await fillOwnerStep(user);
      await fillCompanyStep(user);
      const dayInput = screen.getByLabelText("Year end day");
      await user.clear(dayInput);
      await user.type(dayInput, yearEndDay);
      await user.click(screen.getByRole("button", { name: "Create profile" }));

      expect(
        screen.getByText("Enter a valid year end day."),
      ).toBeInTheDocument();
      expect(fetchImpl).not.toHaveBeenCalled();
    },
  );
});

function renderRegister(initialEntry = "/register") {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={queryClient}>
        <Routes>
          <Route path="/register" element={<RegisterScreen />} />
          <Route path="/" element={<h1>Dashboard</h1>} />
        </Routes>
        <LocationProbe />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

async function fillOwnerStep(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Email"), "owner@example.com");
  await user.type(screen.getByLabelText("Name"), "N. Meyer");
  await user.type(screen.getByLabelText("Password"), "correct1234");
  await user.click(screen.getByRole("button", { name: "Continue" }));
  expect(screen.getByLabelText("Trading name")).toBeInTheDocument();
}

async function fillCompanyStep(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Trading name"), "Ledgerly Consulting");
  await user.type(screen.getByLabelText("Legal name"), "NPM Limited");
  await user.type(screen.getByLabelText("Company number"), "137792C");
  await user.type(
    screen.getByLabelText("Registered office line 1"),
    "12 Quay Street",
  );
  await user.type(screen.getByLabelText("Registered office locality"), "Douglas");
  await user.type(
    screen.getByLabelText("Registered office postal code"),
    "IM1 1AA",
  );
  await user.type(screen.getByLabelText("Incorporation date"), "2025-04-03");
  await user.type(screen.getByLabelText("Director 1 name"), "N. Meyer");
  await user.type(
    screen.getByLabelText("Director 1 appointed date"),
    "2025-04-03",
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

function pathFromRequest(input: RequestInfo | URL) {
  if (input instanceof Request) {
    return new URL(input.url, "http://localhost").pathname;
  }

  return new URL(String(input), "http://localhost").pathname;
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

function problemResponse(
  title: string,
  status: number,
  detail?: string,
  errors?: Array<{ detail: string; pointer: string }>,
) {
  return jsonResponse(
    {
      detail,
      errors,
      status,
      title,
      type: "https://ledgerly.local/problems/test",
    },
    status,
    "application/problem+json",
  );
}

function registerResult() {
  return {
    profile: {
      bank_details: {
        account_name: "",
        account_number: "",
        sort_code: "",
      },
      company_number: "137792C",
      incorporation_date: "2025-04-03",
      is_vat_registered: false,
      legal_name: "NPM Limited",
      logo_asset_id: null,
      logo_asset_url: null,
      registered_office: {
        country: "IM",
        line1: "12 Quay Street",
        line2: "",
        locality: "Douglas",
        postal_code: "IM1 1AA",
        region: "",
      },
      shareholders: [],
      trading_name: "Ledgerly Consulting",
      vat_number: null,
      year_end: {
        day: 31,
        month: 3,
      },
    },
    user: {
      created_at: "2026-07-07T12:00:00Z",
      email: "owner@example.com",
      id: 1,
      name: "N. Meyer",
    },
  };
}
