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
  createIdentityPAT,
  getIdentityProfile,
  getIdentityPATs,
  patchIdentityProfile,
  replaceIdentityLogo,
  revokeIdentityPAT,
  type IdentityPAT,
  type IdentityPATCreateRequest,
  type IdentityProfile,
  type IdentityProfilePatch,
} from "@/api/identity";
import {
  archiveInvoicingClient,
  createInvoicingClient,
  getInvoicingClients,
  patchInvoicingClient,
  type InvoicingClient,
  type InvoicingClientRequest,
  type InvoicingMoneyAmount,
} from "@/api/invoicing";
import { getJurisdictionPack } from "@/api/jurisdiction";
import { queryKeys } from "@/api/queryKeys";
import {
  AmountText,
  Badge,
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageTitle,
  Select,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
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
  directors: DirectorFormState[];
  incorporationDate: string;
  legalName: string;
  line1: string;
  line2: string;
  locality: string;
  postalCode: string;
  region: string;
  isVATRegistered: boolean;
  tradingName: string;
  vatNumber: string;
  yearEndDay: string;
  yearEndMonth: string;
};

type DirectorFormState = {
  appointedDate: string;
  isChair: boolean;
  name: string;
};

type CompanyTextField = Exclude<
  keyof CompanyFormState,
  "directors" | "isVATRegistered"
>;

type ClientCurrency = InvoicingClient["default_currency"];
type ClientVATTreatment = InvoicingClient["vat_treatment"];

type ClientFormState = {
  country: string;
  dayRateAmount: string;
  defaultCurrency: ClientCurrency;
  email: string;
  line1: string;
  line2: string;
  locality: string;
  name: string;
  postalCode: string;
  region: string;
  retainerAmount: string;
  termsDays: string;
  vatNumber: string;
  vatTreatment: ClientVATTreatment;
};

type PATFormState = {
  expiresAt: string;
  name: string;
  scope: IdentityPATCreateRequest["scope"];
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
          <Route path="jurisdiction" element={<JurisdictionSettings />} />
          <Route path="clients" element={<ClientsSettings />} />
          <Route path="users" element={<UsersSettings />} />
          {settingsItems
            .slice(1)
            .filter(
              (item) =>
                item.path !== "jurisdiction" &&
                item.path !== "clients" &&
                item.path !== "users",
            )
            .map((item) => (
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

function JurisdictionSettings() {
  const packQuery = useQuery({
    queryFn: getJurisdictionPack,
    queryKey: queryKeys.jurisdiction.pack(),
  });
  const pack = packQuery.data;

  if (packQuery.isPending) {
    return (
      <div className="settings-detail">
        <PageTitle id="settings-page-title">Jurisdiction</PageTitle>
        <Card title="Rules pack">
          <p className="type-secondary">Loading jurisdiction pack.</p>
        </Card>
      </div>
    );
  }

  if (packQuery.isError) {
    return (
      <div className="settings-detail">
        <PageTitle id="settings-page-title">Jurisdiction</PageTitle>
        <Card title="Rules pack">
          <ProblemAlert
            error={packQuery.error}
            fallbackTitle="Unable to load jurisdiction pack"
          />
        </Card>
      </div>
    );
  }

  if (!pack) {
    return (
      <div className="settings-detail">
        <PageTitle id="settings-page-title">Jurisdiction</PageTitle>
        <Card title="Rules pack">
          <p className="type-secondary">Loading jurisdiction pack.</p>
        </Card>
      </div>
    );
  }

  return (
    <div className="settings-detail">
      <PageTitle id="settings-page-title">Jurisdiction</PageTitle>
      <Card
        actions={<Badge variant="neutral">v{pack.meta.version}</Badge>}
        title={`${pack.meta.name} rules pack`}
      >
        <div className="jurisdiction-pack">
          <div className="jurisdiction-pack__meta">
            <strong>{pack.meta.name}</strong>
            <span>{pack.meta.id}</span>
          </div>
          <ul
            aria-label="Jurisdiction rule summaries"
            className="jurisdiction-rule-list"
          >
            {pack.rule_summaries.map((summary) => (
              <li className="jurisdiction-rule-row" key={summary.id}>
                <span className="jurisdiction-rule-row__label">
                  {summary.label}
                </span>
                <span className="jurisdiction-rule-row__summary">
                  {summary.summary}
                </span>
              </li>
            ))}
          </ul>
          <p className="jurisdiction-pack__note">
            rules packs are installable modules — adding a jurisdiction adds a
            pack
          </p>
        </div>
      </Card>
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
  const isCreatingProfile = profileNotFound;
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
  const vatNumberWithoutRegistration =
    !form.isVATRegistered && form.vatNumber.trim() !== "";
  let submitLabel = isCreatingProfile
    ? "Create company profile"
    : "Save changes";
  if (saveMutation.isPending) {
    submitLabel = isCreatingProfile ? "Creating" : "Saving";
  }

  function handleFieldChange(field: CompanyTextField) {
    return (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormDraft((current) => ({
        ...(current ?? profileToForm(profile)),
        [field]: event.target.value,
      }));
    };
  }

  function handleVATRegisteredChange(event: ChangeEvent<HTMLInputElement>) {
    setFormDraft((current) => ({
      ...(current ?? profileToForm(profile)),
      isVATRegistered: event.target.checked,
    }));
  }

  function handleDirectorFieldChange(
    index: number,
    field: "appointedDate" | "name",
  ) {
    return (event: ChangeEvent<HTMLInputElement>) => {
      const value = event.target.value;
      setFormDraft((current) => {
        const next = current ?? profileToForm(profile);
        return {
          ...next,
          directors: next.directors.map((director, directorIndex) =>
            directorIndex === index
              ? { ...director, [field]: value }
              : director,
          ),
        };
      });
    };
  }

  function handleDirectorChairChange(index: number) {
    return (event: ChangeEvent<HTMLInputElement>) => {
      const checked = event.target.checked;
      setFormDraft((current) => {
        const next = current ?? profileToForm(profile);
        return {
          ...next,
          directors: next.directors.map((director, directorIndex) => ({
            ...director,
            isChair: checked && directorIndex === index,
          })),
        };
      });
    };
  }

  function handleAddDirector() {
    setFormDraft((current) => {
      const next = current ?? profileToForm(profile);
      return {
        ...next,
        directors: [...next.directors, emptyDirectorForm()],
      };
    });
  }

  function handleRemoveDirector(index: number) {
    setFormDraft((current) => {
      const next = current ?? profileToForm(profile);
      return {
        ...next,
        directors: next.directors.filter(
          (_director, directorIndex) => directorIndex !== index,
        ),
      };
    });
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
            {submitLabel}
          </Button>
        }
        title={isCreatingProfile ? "Create company profile" : "Company identity"}
      >
        <div className="company-settings">
          {isCreatingProfile ? null : (
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
          )}
          {!isCreatingProfile && logoProblem ? (
            <ProblemPanel problem={logoProblem} />
          ) : null}
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
            {isCreatingProfile ? null : (
              <div className="vat-registration-group company-field-grid__wide">
                <div className="vat-registration-toggle">
                  <input
                    aria-describedby="company-vat-registration-helper"
                    checked={form.isVATRegistered}
                    id="company-vat-registered"
                    name="is_vat_registered"
                    onChange={handleVATRegisteredChange}
                    type="checkbox"
                  />
                  <div className="vat-registration-toggle__body">
                    <label
                      className="ui-field__label"
                      htmlFor="company-vat-registered"
                    >
                      VAT registered
                    </label>
                    <span
                      className="ui-field__helper"
                      id="company-vat-registration-helper"
                    >
                      Use when the company is registered with Isle of Man
                      Customs & Excise.
                    </span>
                  </div>
                </div>
                <Field
                  helperText={
                    vatNumberWithoutRegistration ? (
                      <span className="vat-registration-warning">
                        VAT number is present while VAT registered is off.
                      </span>
                    ) : (
                      "Appears on VAT invoices and reports when registration is on."
                    )
                  }
                  label="VAT number"
                >
                  <Input
                    aria-label="VAT number"
                    invalid={vatNumberWithoutRegistration}
                    name="vat_number"
                    onChange={handleFieldChange("vatNumber")}
                    placeholder="Not registered"
                    value={form.vatNumber}
                  />
                </Field>
              </div>
            )}
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
            <div className="ui-field company-field-grid__wide">
              <div className="director-editor-heading">
                <span className="ui-field__label" id="directors-label">
                  Directors
                </span>
                <Button onClick={handleAddDirector} size="small" type="button">
                  + Add director
                </Button>
              </div>
              <div
                aria-labelledby="directors-label"
                className="director-editor"
                role="group"
              >
                {form.directors.length === 0 ? (
                  <p className="type-secondary">No directors recorded.</p>
                ) : null}
                {form.directors.map((director, index) => (
                  <div className="director-row" key={index}>
                    <Field label="Name">
                      <Input
                        aria-label={`Director ${index + 1} name`}
                        onChange={handleDirectorFieldChange(index, "name")}
                        required
                        value={director.name}
                      />
                    </Field>
                    <Field label="Appointed">
                      <Input
                        aria-label={`Director ${index + 1} appointed date`}
                        onChange={handleDirectorFieldChange(
                          index,
                          "appointedDate",
                        )}
                        type="date"
                        value={director.appointedDate}
                      />
                    </Field>
                    <label className="director-chair-toggle">
                      <input
                        checked={director.isChair}
                        onChange={handleDirectorChairChange(index)}
                        type="checkbox"
                      />
                      <span>Chair</span>
                    </label>
                    <Button
                      aria-label={`Remove director ${
                        director.name.trim() || index + 1
                      }`}
                      onClick={() => handleRemoveDirector(index)}
                      size="small"
                      type="button"
                      variant="secondary"
                    >
                      Remove
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          </div>
          {isCreatingProfile ? null : (
            <div className="company-profile-summary">
              <span>
                {profile ? addressSummary(profile.registered_office) : ""}
              </span>
              <span>{yearEndSummary}</span>
            </div>
          )}
        </div>
      </Card>
    </form>
  );
}

function ClientsSettings() {
  const queryClient = useQueryClient();
  const clientsQuery = useQuery({
    queryFn: () => getInvoicingClients(false),
    queryKey: queryKeys.invoicing.clients(false),
  });
  const clients = clientsQuery.data?.clients ?? [];
  const [editingClientId, setEditingClientId] = useState<string | null>(null);
  const [formDraft, setFormDraft] = useState<ClientFormState>(
    emptyClientForm,
  );

  const saveMutation = useMutation({
    mutationFn: (request: InvoicingClientRequest) =>
      editingClientId
        ? patchInvoicingClient(editingClientId, request)
        : createInvoicingClient(request),
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.invoicing.clients(false),
      });
    },
    onSuccess: () => {
      setEditingClientId(null);
      setFormDraft(emptyClientForm());
    },
  });
  const archiveMutation = useMutation({
    mutationFn: archiveInvoicingClient,
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.invoicing.clients(false),
      });
    },
    onSuccess: (_result, id) => {
      if (editingClientId === id) {
        setEditingClientId(null);
        setFormDraft(emptyClientForm());
      }
    },
  });

  const activeSaveProblem = isApiError(saveMutation.error)
    ? saveMutation.error.problem
    : null;
  const activeArchiveProblem = isApiError(archiveMutation.error)
    ? archiveMutation.error.problem
    : null;

  function handleAddClient() {
    setEditingClientId(null);
    setFormDraft(emptyClientForm());
  }

  function handleEditClient(client: InvoicingClient) {
    setEditingClientId(client.id);
    setFormDraft(clientToForm(client));
  }

  function handleClientFieldChange(field: keyof ClientFormState) {
    return (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormDraft((current) => ({
        ...current,
        [field]: event.target.value,
      }));
    };
  }

  function handleClientSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveMutation.mutate(formToClientRequest(formDraft));
  }

  return (
    <div className="settings-detail">
      <PageTitle id="settings-page-title">Clients</PageTitle>
      <Card
        actions={
          <Button onClick={handleAddClient} size="small" type="button">
            + Add client
          </Button>
        }
        title="Clients"
      >
        <div className="clients-settings">
          {clientsQuery.isPending ? (
            <p className="type-secondary">Loading clients.</p>
          ) : null}
          {clientsQuery.isError ? (
            <ProblemAlert error={clientsQuery.error} />
          ) : null}
          {activeArchiveProblem ? (
            <ProblemPanel problem={activeArchiveProblem} />
          ) : null}
          {!clientsQuery.isPending && clients.length === 0 ? (
            <EmptyState icon="+" title="No clients">
              Add a client before raising invoices.
            </EmptyState>
          ) : null}
          {clients.length > 0 ? (
            <Table className="clients-table">
              <TableHead>
                <TableRow>
                  <TableHeaderCell>Client</TableHeaderCell>
                  <TableHeaderCell>Currency</TableHeaderCell>
                  <TableHeaderCell>Retainer / day-rate</TableHeaderCell>
                  <TableHeaderCell>Terms + VAT</TableHeaderCell>
                  <TableHeaderCell align="right">Actions</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {clients.map((client) => (
                  <TableRow key={client.id}>
                    <TableCell>
                      <div className="client-list-name">
                        <strong>{client.name}</strong>
                        <span>{clientAddressSummary(client)}</span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          client.default_currency === "EUR" ? "paid" : "sent"
                        }
                      >
                        {client.default_currency}
                      </Badge>
                    </TableCell>
                    <TableCell>{clientBillingSummary(client)}</TableCell>
                    <TableCell>
                      Net {client.terms_days} ·{" "}
                      {vatTreatmentLabel(client.vat_treatment)}
                    </TableCell>
                    <TableCell align="right">
                      <div className="client-row-actions">
                        <Button
                          onClick={() => handleEditClient(client)}
                          size="small"
                          type="button"
                          variant="secondary"
                        >
                          Edit
                        </Button>
                        <Button
                          disabled={archiveMutation.isPending}
                          onClick={() => archiveMutation.mutate(client.id)}
                          size="small"
                          type="button"
                          variant="secondary"
                        >
                          Archive
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : null}
        </div>
      </Card>
      <form className="settings-detail" onSubmit={handleClientSubmit}>
        <Card
          actions={
            <Button disabled={saveMutation.isPending} size="small" type="submit">
              {saveMutation.isPending ? "Saving" : "Save client"}
            </Button>
          }
          title={editingClientId ? "Edit client" : "Add client"}
        >
          <div className="client-form">
            {activeSaveProblem ? (
              <ProblemPanel problem={activeSaveProblem} />
            ) : null}
            <div className="client-field-grid">
              <Field label="Client name">
                <Input
                  name="name"
                  onChange={handleClientFieldChange("name")}
                  required
                  value={formDraft.name}
                />
              </Field>
              <Field label="Billing email">
                <Input
                  inputMode="email"
                  name="email"
                  onChange={handleClientFieldChange("email")}
                  type="email"
                  value={formDraft.email}
                />
              </Field>
              <Field label="Currency">
                <Select
                  name="default_currency"
                  onChange={handleClientFieldChange("defaultCurrency")}
                  value={formDraft.defaultCurrency}
                >
                  <option value="EUR">EUR</option>
                  <option value="GBP">GBP</option>
                </Select>
              </Field>
              <Field label="Terms">
                <Select
                  name="terms_days"
                  onChange={handleClientFieldChange("termsDays")}
                  value={formDraft.termsDays}
                >
                  <option value="14">Net 14</option>
                  <option value="30">Net 30</option>
                </Select>
              </Field>
              <Field label="VAT treatment">
                <Select
                  name="vat_treatment"
                  onChange={handleClientFieldChange("vatTreatment")}
                  value={formDraft.vatTreatment}
                >
                  <option value="domestic">Domestic</option>
                  <option value="reverse-charge-eu-b2b">
                    Reverse charge EU B2B
                  </option>
                </Select>
              </Field>
              <Field label="VAT number">
                <Input
                  name="vat_number"
                  onChange={handleClientFieldChange("vatNumber")}
                  value={formDraft.vatNumber}
                />
              </Field>
              <Field label="Monthly retainer">
                <Input
                  inputMode="decimal"
                  min="0"
                  name="retainer_amount"
                  onChange={handleClientFieldChange("retainerAmount")}
                  step="0.01"
                  type="number"
                  value={formDraft.retainerAmount}
                />
              </Field>
              <Field label="Day rate">
                <Input
                  inputMode="decimal"
                  min="0"
                  name="day_rate"
                  onChange={handleClientFieldChange("dayRateAmount")}
                  step="0.01"
                  type="number"
                  value={formDraft.dayRateAmount}
                />
              </Field>
              <div className="ui-field client-field-grid__wide">
                <span className="ui-field__label" id="client-address-label">
                  Billing address
                </span>
                <div
                  aria-labelledby="client-address-label"
                  className="client-address-grid"
                  role="group"
                >
                  <Input
                    aria-label="Client address line 1"
                    onChange={handleClientFieldChange("line1")}
                    placeholder="Address line 1"
                    required
                    value={formDraft.line1}
                  />
                  <Input
                    aria-label="Client address line 2"
                    onChange={handleClientFieldChange("line2")}
                    placeholder="Address line 2"
                    value={formDraft.line2}
                  />
                  <Input
                    aria-label="Client address locality"
                    onChange={handleClientFieldChange("locality")}
                    placeholder="Town or city"
                    required
                    value={formDraft.locality}
                  />
                  <Input
                    aria-label="Client address region"
                    onChange={handleClientFieldChange("region")}
                    placeholder="Region"
                    value={formDraft.region}
                  />
                  <Input
                    aria-label="Client address postal code"
                    onChange={handleClientFieldChange("postalCode")}
                    placeholder="Postcode"
                    value={formDraft.postalCode}
                  />
                  <Input
                    aria-label="Client address country"
                    onChange={handleClientFieldChange("country")}
                    placeholder="Country code"
                    required
                    value={formDraft.country}
                  />
                </div>
              </div>
            </div>
          </div>
        </Card>
      </form>
    </div>
  );
}

function UsersSettings() {
  const queryClient = useQueryClient();
  const [formDraft, setFormDraft] = useState<PATFormState>({
    expiresAt: "",
    name: "",
    scope: "read-only",
  });
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const patsQuery = useQuery({
    queryFn: getIdentityPATs,
    queryKey: queryKeys.identity.pats(),
  });
  const tokens = patsQuery.data?.tokens ?? [];
  const createMutation = useMutation({
    mutationFn: createIdentityPAT,
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.identity.pats(),
      });
    },
    onSuccess: (response) => {
      setCreatedToken(response.token);
      setFormDraft({ expiresAt: "", name: "", scope: "read-only" });
    },
  });
  const revokeMutation = useMutation({
    mutationFn: revokeIdentityPAT,
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.identity.pats(),
      });
    },
  });

  const createProblem = isApiError(createMutation.error)
    ? createMutation.error.problem
    : null;
  const revokeProblem = isApiError(revokeMutation.error)
    ? revokeMutation.error.problem
    : null;

  function handlePATFieldChange(field: keyof PATFormState) {
    return (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      setFormDraft((current) => ({
        ...current,
        [field]: event.target.value,
      }));
    };
  }

  function handlePATSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setCreatedToken(null);
    createMutation.mutate(formToPATRequest(formDraft));
  }

  return (
    <div className="settings-detail">
      <PageTitle id="settings-page-title">Users</PageTitle>
      <Card title="Personal access tokens">
        <div className="pat-settings">
          {patsQuery.isPending ? (
            <p className="type-secondary">Loading personal access tokens.</p>
          ) : null}
          {patsQuery.isError ? (
            <ProblemAlert
              error={patsQuery.error}
              fallbackTitle="Unable to load personal access tokens"
            />
          ) : null}
          {revokeProblem ? <ProblemPanel problem={revokeProblem} /> : null}
          {tokens.length === 0 && !patsQuery.isPending ? (
            <EmptyState icon="+" title="No personal access tokens">
              Create a token for CLI or agent access.
            </EmptyState>
          ) : null}
          {tokens.length > 0 ? (
            <Table className="pat-table">
              <TableHead>
                <TableRow>
                  <TableHeaderCell>Name</TableHeaderCell>
                  <TableHeaderCell>Scope</TableHeaderCell>
                  <TableHeaderCell>Last used</TableHeaderCell>
                  <TableHeaderCell>Expires</TableHeaderCell>
                  <TableHeaderCell align="right">Actions</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {tokens.map((token) => (
                  <PATRow
                    key={token.id}
                    onRevoke={(id) => revokeMutation.mutate(id)}
                    token={token}
                    revokePending={revokeMutation.isPending}
                  />
                ))}
              </TableBody>
            </Table>
          ) : null}
        </div>
      </Card>
      <form className="settings-detail" onSubmit={handlePATSubmit}>
        <Card
          actions={
            <Button
              disabled={createMutation.isPending}
              size="small"
              type="submit"
            >
              {createMutation.isPending ? "Creating" : "Create token"}
            </Button>
          }
          title="Create token"
        >
          <div className="pat-form">
            {createProblem ? <ProblemPanel problem={createProblem} /> : null}
            {createdToken ? (
              <Field
                helperText="This value is shown once. Store it before leaving this screen."
                label="New token"
              >
                <Input
                  className="type-mono-numeral"
                  locked
                  readOnly
                  value={createdToken}
                />
              </Field>
            ) : null}
            <div className="pat-field-grid">
              <Field label="Token name">
                <Input
                  name="name"
                  onChange={handlePATFieldChange("name")}
                  required
                  value={formDraft.name}
                />
              </Field>
              <Field label="Scope">
                <Select
                  name="scope"
                  onChange={handlePATFieldChange("scope")}
                  value={formDraft.scope}
                >
                  <option value="read-only">Read-only</option>
                  <option value="full">Full</option>
                </Select>
              </Field>
              <Field label="Expires">
                <Input
                  name="expires_at"
                  onChange={handlePATFieldChange("expiresAt")}
                  type="date"
                  value={formDraft.expiresAt}
                />
              </Field>
            </div>
          </div>
        </Card>
      </form>
    </div>
  );
}

function PATRow({
  onRevoke,
  revokePending,
  token,
}: {
  readonly onRevoke: (id: number) => void;
  readonly revokePending: boolean;
  readonly token: IdentityPAT;
}) {
  return (
    <TableRow>
      <TableCell>
        <strong>{token.name}</strong>
      </TableCell>
      <TableCell>
        <Badge variant={token.scope === "full" ? "sent" : "neutral"}>
          {token.scope === "full" ? "Full" : "Read-only"}
        </Badge>
      </TableCell>
      <TableCell>{formatNullableDateTime(token.last_used_at)}</TableCell>
      <TableCell>{formatNullableDateTime(token.expires_at)}</TableCell>
      <TableCell align="right">
        <Button
          disabled={revokePending}
          onClick={() => onRevoke(token.id)}
          size="small"
          type="button"
          variant="danger"
        >
          Revoke
        </Button>
      </TableCell>
    </TableRow>
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

function ProblemAlert({
  error,
  fallbackTitle = "Unable to load company profile",
}: {
  readonly error: unknown;
  readonly fallbackTitle?: string;
}) {
  if (isApiError(error)) {
    return <ProblemPanel problem={error.problem} />;
  }

  return (
    <div className="problem-alert" role="alert">
      <strong>{fallbackTitle}</strong>
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
    directors: (profile?.directors ?? []).map((director) => ({
      appointedDate: director.appointed_date ?? "",
      isChair: director.is_chair ?? false,
      name: director.name,
    })),
    incorporationDate: profile?.incorporation_date ?? "",
    legalName: profile?.legal_name ?? "",
    line1: profile?.registered_office.line1 ?? "",
    line2: profile?.registered_office.line2 ?? "",
    locality: profile?.registered_office.locality ?? "",
    postalCode: profile?.registered_office.postal_code ?? "",
    region: profile?.registered_office.region ?? "",
    isVATRegistered: profile?.is_vat_registered ?? false,
    tradingName: profile?.trading_name ?? "",
    vatNumber: profile?.vat_number ?? "",
    yearEndDay: String(profile?.year_end.day ?? 31),
    yearEndMonth: String(profile?.year_end.month ?? 3),
  };
}

function formToPatch(form: CompanyFormState): IdentityProfilePatch {
  return {
    company_number: form.companyNumber.trim(),
    directors: form.directors.map((director) => ({
      appointed_date: director.appointedDate || undefined,
      is_chair: director.isChair || undefined,
      name: director.name.trim(),
    })),
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
    is_vat_registered: form.isVATRegistered,
    trading_name: form.tradingName.trim(),
    vat_number: form.vatNumber.trim() || null,
    year_end: {
      day: Number(form.yearEndDay),
      month: Number(form.yearEndMonth),
    },
  };
}

function formToPATRequest(form: PATFormState): IdentityPATCreateRequest {
  return {
    expires_at: form.expiresAt
      ? new Date(`${form.expiresAt}T23:59:59Z`).toISOString()
      : null,
    name: form.name.trim(),
    scope: form.scope,
  };
}

function formatNullableDateTime(value: string | null | undefined) {
  if (!value) {
    return "Never";
  }
  return new Intl.DateTimeFormat("en-GB", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
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
    is_vat_registered:
      patch.is_vat_registered ?? profile.is_vat_registered,
    legal_name: patch.legal_name ?? profile.legal_name,
    logo_asset_id:
      patch.logo_asset_id === undefined
        ? profile.logo_asset_id
        : patch.logo_asset_id,
    registered_office: patch.registered_office ?? profile.registered_office,
    shareholders: patch.shareholders ?? profile.shareholders,
    directors: patch.directors ?? profile.directors,
    trading_name: patch.trading_name ?? profile.trading_name,
    vat_number:
      patch.vat_number === undefined ? profile.vat_number : patch.vat_number,
    year_end: patch.year_end ?? profile.year_end,
  };
}

function emptyDirectorForm(): DirectorFormState {
  return {
    appointedDate: "",
    isChair: false,
    name: "",
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

function emptyClientForm(): ClientFormState {
  return {
    country: "",
    dayRateAmount: "",
    defaultCurrency: "GBP",
    email: "",
    line1: "",
    line2: "",
    locality: "",
    name: "",
    postalCode: "",
    region: "",
    retainerAmount: "",
    termsDays: "30",
    vatNumber: "",
    vatTreatment: "domestic",
  };
}

function clientToForm(client: InvoicingClient): ClientFormState {
  return {
    country: client.address.country,
    dayRateAmount: moneyInputValue(client.day_rate),
    defaultCurrency: client.default_currency,
    email: client.email ?? "",
    line1: client.address.line1,
    line2: client.address.line2,
    locality: client.address.locality,
    name: client.name,
    postalCode: client.address.postal_code,
    region: client.address.region,
    retainerAmount: moneyInputValue(client.retainer_amount),
    termsDays: String(client.terms_days),
    vatNumber: client.vat_number ?? "",
    vatTreatment: client.vat_treatment,
  };
}

function formToClientRequest(form: ClientFormState): InvoicingClientRequest {
  return {
    address: {
      country: form.country.trim(),
      line1: form.line1.trim(),
      line2: form.line2.trim(),
      locality: form.locality.trim(),
      postal_code: form.postalCode.trim(),
      region: form.region.trim(),
    },
    day_rate: moneyFromInput(form.dayRateAmount, form.defaultCurrency),
    default_currency: form.defaultCurrency,
    email: form.email.trim() || null,
    name: form.name.trim(),
    retainer_amount: moneyFromInput(
      form.retainerAmount,
      form.defaultCurrency,
    ),
    terms_days: clientTermsDays(form.termsDays),
    vat_number: form.vatNumber.trim() || null,
    vat_treatment: form.vatTreatment,
  };
}

function moneyFromInput(
  value: string,
  currency: ClientCurrency,
): InvoicingMoneyAmount | null {
  const amountMinor = parseMoneyInput(value);
  if (amountMinor === null) {
    return null;
  }
  return {
    amount_minor: amountMinor,
    currency,
  };
}

function clientTermsDays(value: string): InvoicingClientRequest["terms_days"] {
  return value === "14" ? 14 : 30;
}

function parseMoneyInput(value: string) {
  const trimmed = value.trim();
  if (trimmed === "") {
    return null;
  }
  const match = /^(\d+)(?:\.(\d{0,2}))?$/.exec(trimmed);
  if (!match) {
    return 0;
  }
  const whole = Number(match[1]);
  const fractional = Number((match[2] ?? "").padEnd(2, "0"));
  if (!Number.isSafeInteger(whole) || !Number.isSafeInteger(fractional)) {
    return 0;
  }
  return whole * 100 + fractional;
}

function moneyInputValue(amount: InvoicingMoneyAmount | null) {
  if (!amount) {
    return "";
  }
  const sign = amount.amount_minor < 0 ? "-" : "";
  const absolute = Math.abs(amount.amount_minor);
  const whole = Math.floor(absolute / 100);
  const fractional = String(absolute % 100).padStart(2, "0");
  return `${sign}${whole}.${fractional}`;
}

function clientBillingSummary(client: InvoicingClient) {
  const items = [];
  if (client.retainer_amount) {
    items.push(
      <span key="retainer">
        Retainer <MoneyAmountText amount={client.retainer_amount} /> / month
      </span>,
    );
  }
  if (client.day_rate) {
    items.push(
      <span key="day-rate">
        Day rate <MoneyAmountText amount={client.day_rate} /> / day
      </span>,
    );
  }
  if (items.length === 0) {
    return <span className="type-secondary">Ad-hoc</span>;
  }
  return <div className="client-billing-summary">{items}</div>;
}

function MoneyAmountText({
  amount,
}: {
  readonly amount: InvoicingMoneyAmount;
}) {
  return (
    <AmountText
      amountMinor={amount.amount_minor}
      currency={amount.currency}
    />
  );
}

function clientAddressSummary(client: InvoicingClient) {
  const place = [
    client.address.locality,
    countryName(client.address.country),
  ].filter(Boolean);
  const vat = client.vat_number ? `VAT ${client.vat_number}` : "";
  return [...place, client.email, vat].filter(Boolean).join(" · ");
}

function countryName(country: string) {
  switch (country.toUpperCase()) {
    case "DE":
      return "Germany";
    case "GB":
      return "United Kingdom";
    case "IM":
      return "Isle of Man";
    default:
      return country.toUpperCase();
  }
}

function vatTreatmentLabel(treatment: ClientVATTreatment) {
  switch (treatment) {
    case "reverse-charge-eu-b2b":
      return "reverse charge";
    case "domestic":
      return "domestic";
    default:
      return treatment;
  }
}

function initialFor(value: string) {
  const trimmed = value.trim();
  return (trimmed[0] ?? "L").toUpperCase();
}
