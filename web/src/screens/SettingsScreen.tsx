import {
  ChangeEvent,
  DragEvent,
  FormEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, NavLink, Route, Routes } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  getIdentityProfile,
  patchIdentityProfile,
  replaceIdentityLogo,
  type IdentityProfile,
  type IdentityProfilePatch,
} from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
import {
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageTitle,
  Select,
} from "@/components";

const settingsItems = [
  { label: "Company", path: "company", title: "Company" },
  {
    label: "Jurisdiction",
    path: "jurisdiction",
    title: "Jurisdiction",
  },
  { label: "Clients", path: "clients", title: "Clients" },
  {
    label: "Invoicing defaults",
    path: "invoicing-defaults",
    title: "Invoicing defaults",
  },
  {
    label: "Bank connections",
    path: "bank-connections",
    title: "Bank connections",
  },
  { label: "Users", path: "users", title: "Users" },
] as const;

const monthOptions = [
  { label: "January", value: 1 },
  { label: "February", value: 2 },
  { label: "March", value: 3 },
  { label: "April", value: 4 },
  { label: "May", value: 5 },
  { label: "June", value: 6 },
  { label: "July", value: 7 },
  { label: "August", value: 8 },
  { label: "September", value: 9 },
  { label: "October", value: 10 },
  { label: "November", value: 11 },
  { label: "December", value: 12 },
] as const;

type CompanyFormState = {
  companyNumber: string;
  country: string;
  incorporationDate: string;
  legalName: string;
  line1: string;
  line2: string;
  locality: string;
  postalCode: string;
  region: string;
  tradingName: string;
  vatNumber: string;
  yearEndDay: string;
  yearEndMonth: string;
};

export function SettingsScreen() {
  return (
    <div className="settings-shell">
      <nav className="settings-shell__nav" aria-label="Settings">
        {settingsItems.map((item) => (
          <NavLink
            className={({ isActive }) =>
              isActive
                ? "settings-shell__link settings-shell__link--active"
                : "settings-shell__link"
            }
            key={item.path}
            to={`/settings/${item.path}`}
          >
            {item.label}
          </NavLink>
        ))}
      </nav>
      <section
        className="settings-shell__content"
        aria-labelledby="settings-page-title"
      >
        <Routes>
          <Route index element={<Navigate replace to="company" />} />
          <Route path="company" element={<CompanySettings />} />
          {settingsItems.slice(1).map((item) => (
            <Route
              element={<ComingSoonSettings title={item.title} />}
              key={item.path}
              path={item.path}
            />
          ))}
          <Route
            path="*"
            element={<ComingSoonSettings title="Settings not found" />}
          />
        </Routes>
      </section>
    </div>
  );
}

function CompanySettings() {
  const queryClient = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [logoPreviewUrl, setLogoPreviewUrl] = useState<string | null>(null);
  const [isDraggingLogo, setIsDraggingLogo] = useState(false);
  const profileQuery = useQuery({
    queryFn: getIdentityProfile,
    queryKey: queryKeys.identity.profile(),
  });
  const profile = profileQuery.data;
  const profileNotFound =
    isApiError(profileQuery.error) && profileQuery.error.status === 404;
  const [formDraft, setFormDraft] = useState<CompanyFormState | null>(null);
  const form = formDraft ?? profileToForm(profile);

  useEffect(() => {
    return () => {
      if (logoPreviewUrl) {
        URL.revokeObjectURL(logoPreviewUrl);
      }
    };
  }, [logoPreviewUrl]);

  const saveMutation = useMutation({
    mutationFn: patchIdentityProfile,
    onError: (_error, _patch, context) => {
      if (context?.previousProfile) {
        queryClient.setQueryData(
          queryKeys.identity.profile(),
          context.previousProfile,
        );
      }
    },
    onMutate: async (patch: IdentityProfilePatch) => {
      await queryClient.cancelQueries({
        queryKey: queryKeys.identity.profile(),
      });
      const previousProfile = queryClient.getQueryData<IdentityProfile>(
        queryKeys.identity.profile(),
      );
      if (previousProfile) {
        queryClient.setQueryData(
          queryKeys.identity.profile(),
          applyProfilePatch(previousProfile, patch),
        );
      }
      return { previousProfile };
    },
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.identity.profile(),
      });
    },
    onSuccess: (updatedProfile) => {
      queryClient.setQueryData(queryKeys.identity.profile(), updatedProfile);
      setFormDraft(null);
    },
  });

  const logoMutation = useMutation({
    mutationFn: replaceIdentityLogo,
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.identity.profile(),
      });
    },
    onSuccess: (upload) => {
      queryClient.setQueryData<IdentityProfile | undefined>(
        queryKeys.identity.profile(),
        (currentProfile) =>
          currentProfile
            ? {
                ...currentProfile,
                logo_asset_id: upload.asset_id,
                logo_asset_url: upload.asset_url,
              }
            : currentProfile,
      );
      setLogoPreviewUrl(null);
    },
  });

  const saveProblem = isApiError(saveMutation.error)
    ? saveMutation.error.problem
    : null;
  const logoProblem = isApiError(logoMutation.error)
    ? logoMutation.error.problem
    : null;
  const logoSrc = logoPreviewUrl ?? profile?.logo_asset_url ?? undefined;
  const companyInitial = initialFor(profile?.trading_name ?? form.tradingName);
  const yearEndSummary = useMemo(() => formatYearEnd(profile), [profile]);

  function handleFieldChange(field: keyof CompanyFormState) {
    return (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormDraft((current) => ({
        ...(current ?? profileToForm(profile)),
        [field]: event.target.value,
      }));
    };
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveMutation.mutate(formToPatch(form));
  }

  function handleFileSelection(event: ChangeEvent<HTMLInputElement>) {
    const [file] = Array.from(event.target.files ?? []);
    if (file) {
      uploadLogo(file);
    }
    event.target.value = "";
  }

  function handleLogoDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setIsDraggingLogo(false);
    const [file] = Array.from(event.dataTransfer.files);
    if (file) {
      uploadLogo(file);
    }
  }

  function uploadLogo(file: File) {
    const previewUrl = URL.createObjectURL(file);
    setLogoPreviewUrl((currentUrl) => {
      if (currentUrl) {
        URL.revokeObjectURL(currentUrl);
      }
      return previewUrl;
    });
    logoMutation.mutate(file);
  }

  if (profileQuery.isPending) {
    return (
      <div className="settings-detail">
        <PageTitle id="settings-page-title">Company</PageTitle>
        <Card title="Company identity">
          <p className="type-secondary">Loading company profile.</p>
        </Card>
      </div>
    );
  }

  if (profileQuery.isError && !profileNotFound) {
    return (
      <div className="settings-detail">
        <PageTitle id="settings-page-title">Company</PageTitle>
        <Card title="Company identity">
          <ProblemAlert error={profileQuery.error} />
        </Card>
      </div>
    );
  }

  return (
    <form className="settings-detail" onSubmit={handleSubmit}>
      <PageTitle id="settings-page-title">Company</PageTitle>
      <Card
        actions={
          <Button disabled={saveMutation.isPending} size="small" type="submit">
            {saveMutation.isPending ? "Saving" : "Save changes"}
          </Button>
        }
        title="Company identity"
      >
        <div className="company-settings">
          <div className="company-logo-row">
            <div
              aria-label="Company logo drop area"
              className={
                isDraggingLogo
                  ? "company-logo-drop company-logo-drop--active"
                  : "company-logo-drop"
              }
              onDragLeave={() => setIsDraggingLogo(false)}
              onDragOver={(event) => {
                event.preventDefault();
                setIsDraggingLogo(true);
              }}
              onDrop={handleLogoDrop}
            >
              {logoSrc ? (
                <img alt="Company logo preview" src={logoSrc} />
              ) : (
                <span>{companyInitial}</span>
              )}
            </div>
            <div className="company-logo-actions">
              <Button
                disabled={logoMutation.isPending}
                onClick={() => fileInputRef.current?.click()}
                size="small"
                type="button"
                variant="secondary"
              >
                {logoMutation.isPending ? "Uploading" : "Replace logo..."}
              </Button>
              <input
                aria-label="Company logo file"
                accept="image/png,image/jpeg"
                className="company-logo-input"
                onChange={handleFileSelection}
                ref={fileInputRef}
                type="file"
              />
              <span>PNG or JPEG, appears on invoices, vouchers and emails</span>
            </div>
          </div>
          {logoProblem ? <ProblemPanel problem={logoProblem} /> : null}
          {saveProblem ? <ProblemPanel problem={saveProblem} /> : null}
          <div className="company-field-grid">
            <Field label="Trading name">
              <Input
                name="trading_name"
                onChange={handleFieldChange("tradingName")}
                required
                value={form.tradingName}
              />
            </Field>
            <Field label="Legal name">
              <Input
                name="legal_name"
                onChange={handleFieldChange("legalName")}
                required
                value={form.legalName}
              />
            </Field>
            <Field label="Company number">
              <Input
                className="type-mono-numeral"
                name="company_number"
                onChange={handleFieldChange("companyNumber")}
                required
                value={form.companyNumber}
              />
            </Field>
            <Field label="VAT number">
              <Input
                name="vat_number"
                onChange={handleFieldChange("vatNumber")}
                placeholder="Not registered"
                value={form.vatNumber}
              />
            </Field>
            <div className="ui-field company-field-grid__wide">
              <span className="ui-field__label" id="registered-office-label">
                Registered office
              </span>
              <div
                aria-labelledby="registered-office-label"
                className="registered-office-grid"
                role="group"
              >
                <Input
                  aria-label="Registered office line 1"
                  onChange={handleFieldChange("line1")}
                  placeholder="Address line 1"
                  required
                  value={form.line1}
                />
                <Input
                  aria-label="Registered office line 2"
                  onChange={handleFieldChange("line2")}
                  placeholder="Address line 2"
                  value={form.line2}
                />
                <Input
                  aria-label="Registered office locality"
                  onChange={handleFieldChange("locality")}
                  placeholder="Town or city"
                  required
                  value={form.locality}
                />
                <Input
                  aria-label="Registered office region"
                  onChange={handleFieldChange("region")}
                  placeholder="Region"
                  value={form.region}
                />
                <Input
                  aria-label="Registered office postal code"
                  onChange={handleFieldChange("postalCode")}
                  placeholder="Postcode"
                  value={form.postalCode}
                />
                <Input
                  aria-label="Registered office country"
                  onChange={handleFieldChange("country")}
                  placeholder="Country code"
                  required
                  value={form.country}
                />
              </div>
            </div>
            <Field label="Incorporation date">
              <Input
                name="incorporation_date"
                onChange={handleFieldChange("incorporationDate")}
                required
                type="date"
                value={form.incorporationDate}
              />
            </Field>
            <div className="ui-field">
              <span className="ui-field__label" id="year-end-label">
                Year end
              </span>
              <div
                aria-labelledby="year-end-label"
                className="year-end-fields"
                role="group"
              >
                <Input
                  aria-label="Year end day"
                  max={31}
                  min={1}
                  onChange={handleFieldChange("yearEndDay")}
                  required
                  type="number"
                  value={form.yearEndDay}
                />
                <Select
                  aria-label="Year end month"
                  onChange={handleFieldChange("yearEndMonth")}
                  required
                  value={form.yearEndMonth}
                >
                  {monthOptions.map((month) => (
                    <option key={month.value} value={month.value}>
                      {month.label}
                    </option>
                  ))}
                </Select>
              </div>
            </div>
          </div>
          <div className="company-profile-summary">
            <span>
              {profile ? addressSummary(profile.registered_office) : ""}
            </span>
            <span>{yearEndSummary}</span>
          </div>
        </div>
      </Card>
    </form>
  );
}

function ComingSoonSettings({ title }: { readonly title: string }) {
  return (
    <div className="settings-detail">
      <PageTitle id="settings-page-title">{title}</PageTitle>
      <EmptyState icon="+" title="Coming soon">
        This settings area is owned by its module.
      </EmptyState>
    </div>
  );
}

function ProblemAlert({ error }: { readonly error: unknown }) {
  if (isApiError(error)) {
    return <ProblemPanel problem={error.problem} />;
  }

  return (
    <div className="problem-alert" role="alert">
      <strong>Unable to load company profile</strong>
    </div>
  );
}

function ProblemPanel({
  problem,
}: {
  readonly problem: { detail?: string; title: string };
}) {
  return (
    <div className="problem-alert" role="alert">
      <strong>{problem.title}</strong>
      {problem.detail ? <span>{problem.detail}</span> : null}
    </div>
  );
}

function profileToForm(profile: IdentityProfile | undefined): CompanyFormState {
  return {
    companyNumber: profile?.company_number ?? "",
    country: profile?.registered_office.country ?? "",
    incorporationDate: profile?.incorporation_date ?? "",
    legalName: profile?.legal_name ?? "",
    line1: profile?.registered_office.line1 ?? "",
    line2: profile?.registered_office.line2 ?? "",
    locality: profile?.registered_office.locality ?? "",
    postalCode: profile?.registered_office.postal_code ?? "",
    region: profile?.registered_office.region ?? "",
    tradingName: profile?.trading_name ?? "",
    vatNumber: profile?.vat_number ?? "",
    yearEndDay: String(profile?.year_end.day ?? 31),
    yearEndMonth: String(profile?.year_end.month ?? 3),
  };
}

function formToPatch(form: CompanyFormState): IdentityProfilePatch {
  return {
    company_number: form.companyNumber.trim(),
    incorporation_date: form.incorporationDate,
    legal_name: form.legalName.trim(),
    registered_office: {
      country: form.country.trim(),
      line1: form.line1.trim(),
      line2: form.line2.trim(),
      locality: form.locality.trim(),
      postal_code: form.postalCode.trim(),
      region: form.region.trim(),
    },
    trading_name: form.tradingName.trim(),
    vat_number: form.vatNumber.trim() || null,
    year_end: {
      day: Number(form.yearEndDay),
      month: Number(form.yearEndMonth),
    },
  };
}

function applyProfilePatch(
  profile: IdentityProfile,
  patch: IdentityProfilePatch,
): IdentityProfile {
  return {
    ...profile,
    bank_details: patch.bank_details ?? profile.bank_details,
    company_number: patch.company_number ?? profile.company_number,
    incorporation_date: patch.incorporation_date ?? profile.incorporation_date,
    legal_name: patch.legal_name ?? profile.legal_name,
    logo_asset_id:
      patch.logo_asset_id === undefined
        ? profile.logo_asset_id
        : patch.logo_asset_id,
    registered_office: patch.registered_office ?? profile.registered_office,
    shareholders: patch.shareholders ?? profile.shareholders,
    trading_name: patch.trading_name ?? profile.trading_name,
    vat_number:
      patch.vat_number === undefined ? profile.vat_number : patch.vat_number,
    year_end: patch.year_end ?? profile.year_end,
  };
}

function addressSummary(profile: IdentityProfile["registered_office"]) {
  return [
    profile.line1,
    profile.line2,
    profile.locality,
    profile.region,
    profile.postal_code,
    profile.country,
  ]
    .filter(Boolean)
    .join(", ");
}

function formatYearEnd(profile: IdentityProfile | undefined) {
  if (!profile) {
    return "Year end";
  }

  const month =
    monthOptions.find((option) => option.value === profile.year_end.month)
      ?.label ?? String(profile.year_end.month);
  return `Year end ${profile.year_end.day} ${month}`;
}

function initialFor(value: string) {
  const trimmed = value.trim();
  return (trimmed[0] ?? "L").toUpperCase();
}
