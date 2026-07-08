import { ChangeEvent, FormEvent, ReactNode, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  registerIdentityWithProfile,
  type IdentityRegisterWithProfileRequest,
} from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
import { Button, Field, Input, Select } from "@/components";

type RegisterFormState = {
  readonly companyNumber: string;
  readonly country: string;
  readonly directors: DirectorFormState[];
  readonly email: string;
  readonly incorporationDate: string;
  readonly legalName: string;
  readonly line1: string;
  readonly line2: string;
  readonly locality: string;
  readonly name: string;
  readonly password: string;
  readonly postalCode: string;
  readonly region: string;
  readonly tradingName: string;
  readonly yearEndDay: string;
  readonly yearEndMonth: string;
};

type DirectorFormState = {
  readonly appointedDate: string;
  readonly isChair: boolean;
  readonly name: string;
};

type RegisterTextField = Exclude<keyof RegisterFormState, "directors">;
type RegisterField = keyof RegisterFormState | "registeredOffice";
type RegisterFieldErrors = Partial<Record<RegisterField, string>>;
type Step = 1 | 2;

const initialForm: RegisterFormState = {
  companyNumber: "",
  country: "IM",
  directors: [{ appointedDate: "", isChair: true, name: "" }],
  email: "",
  incorporationDate: "",
  legalName: "",
  line1: "",
  line2: "",
  locality: "",
  name: "",
  password: "",
  postalCode: "",
  region: "",
  tradingName: "",
  yearEndDay: "31",
  yearEndMonth: "3",
};

const monthOptions = [
  { label: "January", value: "1" },
  { label: "February", value: "2" },
  { label: "March", value: "3" },
  { label: "April", value: "4" },
  { label: "May", value: "5" },
  { label: "June", value: "6" },
  { label: "July", value: "7" },
  { label: "August", value: "8" },
  { label: "September", value: "9" },
  { label: "October", value: "10" },
  { label: "November", value: "11" },
  { label: "December", value: "12" },
] as const;

const apiPointerFields: Record<string, RegisterField> = {
  "/company_number": "companyNumber",
  "/directors": "directors",
  "/email": "email",
  "/incorporation_date": "incorporationDate",
  "/legal_name": "legalName",
  "/name": "name",
  "/password": "password",
  "/registered_office": "registeredOffice",
  "/registered_office/country": "country",
  "/registered_office/line1": "line1",
  "/registered_office/line2": "line2",
  "/registered_office/locality": "locality",
  "/registered_office/postal_code": "postalCode",
  "/registered_office/region": "region",
  "/trading_name": "tradingName",
  "/year_end_day": "yearEndDay",
  "/year_end_month": "yearEndMonth",
};

const stepOneFields: readonly RegisterField[] = ["email", "name", "password"];

export function RegisterScreen() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<RegisterFormState>(initialForm);
  const [fieldErrors, setFieldErrors] = useState<RegisterFieldErrors>({});
  const [step, setStep] = useState<Step>(1);

  const registerMutation = useMutation({
    mutationFn: registerIdentityWithProfile,
    onError: (error) => {
      if (!isApiError(error) || error.status !== 400) {
        return;
      }

      const nextErrors = fieldErrorsFromProblem(error.problem);
      setFieldErrors(nextErrors);
      if (hasStepOneError(nextErrors)) {
        setStep(1);
      }
    },
    onMutate: () => {
      setFieldErrors({});
    },
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.identity.me() }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.identity.profile(),
        }),
      ]);
      navigate("/", { replace: true });
    },
  });

  const problem = isApiError(registerMutation.error)
    ? registerMutation.error.problem
    : null;
  const isRegistrationClosed =
    isApiError(registerMutation.error) && registerMutation.error.status === 403;

  function handleFieldChange(field: RegisterTextField) {
    return (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      const { value } = event.target;
      setForm((current) => ({ ...current, [field]: value }));
      setFieldErrors((current) => omitFieldError(current, field));
      if (registerMutation.error) {
        registerMutation.reset();
      }
    };
  }

  function handleDirectorFieldChange(
    index: number,
    field: "appointedDate" | "name",
  ) {
    return (event: ChangeEvent<HTMLInputElement>) => {
      const { value } = event.target;
      setForm((current) => ({
        ...current,
        directors: current.directors.map((director, directorIndex) =>
          directorIndex === index
            ? { ...director, [field]: value }
            : director,
        ),
      }));
      setFieldErrors((current) => omitFieldError(current, "directors"));
      if (registerMutation.error) {
        registerMutation.reset();
      }
    };
  }

  function handleDirectorChairChange(index: number) {
    return (event: ChangeEvent<HTMLInputElement>) => {
      const checked = event.target.checked;
      setForm((current) => ({
        ...current,
        directors: current.directors.map((director, directorIndex) => ({
          ...director,
          isChair: checked && directorIndex === index,
        })),
      }));
      setFieldErrors((current) => omitFieldError(current, "directors"));
      if (registerMutation.error) {
        registerMutation.reset();
      }
    };
  }

  function handleAddDirector() {
    setForm((current) => ({
      ...current,
      directors: [...current.directors, emptyDirectorForm()],
    }));
    setFieldErrors((current) => omitFieldError(current, "directors"));
  }

  function handleRemoveDirector(index: number) {
    setForm((current) => ({
      ...current,
      directors: current.directors.filter(
        (_director, directorIndex) => directorIndex !== index,
      ),
    }));
    setFieldErrors((current) => omitFieldError(current, "directors"));
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    if (step === 1) {
      const nextErrors = validateIdentityStep(form);
      setFieldErrors(nextErrors);
      if (Object.keys(nextErrors).length === 0) {
        setStep(2);
      }
      return;
    }

    const nextErrors = validateProfileStep(form);
    setFieldErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) {
      return;
    }

    registerMutation.mutate(formToRequest(form));
  }

  return (
    <main className="register-screen" aria-labelledby="register-title">
      <form className="register-card" noValidate onSubmit={handleSubmit}>
        <div className="register-card__header">
          <div>
            <p className="eyebrow">First-run setup</p>
            <h1 id="register-title">Set up Ledgerly</h1>
          </div>
          <Link className="register-card__login-link" to="/login">
            Back to login
          </Link>
        </div>

        <ol className="register-progress" aria-label="Registration progress">
          <ProgressStep current={step === 1} complete={step > 1} index={1}>
            Owner account
          </ProgressStep>
          <ProgressStep current={step === 2} complete={false} index={2}>
            Company profile
          </ProgressStep>
        </ol>

        {problem ? (
          <ProblemPanel closed={isRegistrationClosed} problem={problem} />
        ) : null}

        {step === 1 ? (
          <div className="register-card__fields">
            <Field label="Email" helperText={fieldError(fieldErrors.email)}>
              <Input
                autoComplete="email"
                inputMode="email"
                invalid={Boolean(fieldErrors.email)}
                name="email"
                onChange={handleFieldChange("email")}
                required
                type="email"
                value={form.email}
              />
            </Field>
            <Field label="Name" helperText={fieldError(fieldErrors.name)}>
              <Input
                autoComplete="name"
                invalid={Boolean(fieldErrors.name)}
                name="name"
                onChange={handleFieldChange("name")}
                required
                value={form.name}
              />
            </Field>
            <Field
              label="Password"
              helperText={
                fieldErrors.password ? (
                  fieldError(fieldErrors.password)
                ) : (
                  <span>Use at least 10 characters with letters and numbers.</span>
                )
              }
            >
              <Input
                aria-label="Password"
                autoComplete="new-password"
                invalid={Boolean(fieldErrors.password)}
                name="password"
                onChange={handleFieldChange("password")}
                required
                type="password"
                value={form.password}
              />
            </Field>
          </div>
        ) : (
          <div className="register-card__fields register-card__fields--company">
            <Field
              label="Trading name"
              helperText={fieldError(fieldErrors.tradingName)}
            >
              <Input
                invalid={Boolean(fieldErrors.tradingName)}
                name="trading_name"
                onChange={handleFieldChange("tradingName")}
                required
                value={form.tradingName}
              />
            </Field>
            <Field
              label="Legal name"
              helperText={fieldError(fieldErrors.legalName)}
            >
              <Input
                invalid={Boolean(fieldErrors.legalName)}
                name="legal_name"
                onChange={handleFieldChange("legalName")}
                required
                value={form.legalName}
              />
            </Field>
            <Field
              label="Company number"
              helperText={fieldError(fieldErrors.companyNumber)}
            >
              <Input
                className="type-mono-numeral"
                invalid={Boolean(fieldErrors.companyNumber)}
                name="company_number"
                onChange={handleFieldChange("companyNumber")}
                required
                value={form.companyNumber}
              />
            </Field>
            <div className="ui-field register-card__wide-field">
              <span className="ui-field__label" id="register-office-label">
                Registered office
              </span>
              <div
                aria-labelledby="register-office-label"
                className="register-office-grid"
                role="group"
              >
                <Input
                  aria-label="Registered office line 1"
                  invalid={Boolean(fieldErrors.line1)}
                  onChange={handleFieldChange("line1")}
                  placeholder="Address line 1"
                  required
                  value={form.line1}
                />
                <Input
                  aria-label="Registered office line 2"
                  invalid={Boolean(fieldErrors.line2)}
                  onChange={handleFieldChange("line2")}
                  placeholder="Address line 2"
                  value={form.line2}
                />
                <Input
                  aria-label="Registered office locality"
                  invalid={Boolean(fieldErrors.locality)}
                  onChange={handleFieldChange("locality")}
                  placeholder="Town or city"
                  required
                  value={form.locality}
                />
                <Input
                  aria-label="Registered office region"
                  invalid={Boolean(fieldErrors.region)}
                  onChange={handleFieldChange("region")}
                  placeholder="Region"
                  value={form.region}
                />
                <Input
                  aria-label="Registered office postal code"
                  invalid={Boolean(fieldErrors.postalCode)}
                  onChange={handleFieldChange("postalCode")}
                  placeholder="Postcode"
                  value={form.postalCode}
                />
                <Input
                  aria-label="Registered office country"
                  invalid={Boolean(fieldErrors.country)}
                  onChange={handleFieldChange("country")}
                  placeholder="Country code"
                  required
                  value={form.country}
                />
              </div>
              {fieldError(firstPresentError(fieldErrors, [
                "registeredOffice",
                "line1",
                "line2",
                "locality",
                "region",
                "postalCode",
                "country",
              ]))}
            </div>
            <Field
              label="Incorporation date"
              helperText={fieldError(fieldErrors.incorporationDate)}
            >
              <Input
                invalid={Boolean(fieldErrors.incorporationDate)}
                name="incorporation_date"
                onChange={handleFieldChange("incorporationDate")}
                required
                type="date"
                value={form.incorporationDate}
              />
            </Field>
            <div className="ui-field">
              <span className="ui-field__label" id="register-year-end-label">
                Year end
              </span>
              <div
                aria-labelledby="register-year-end-label"
                className="register-year-end-fields"
                role="group"
              >
                <Input
                  aria-label="Year end day"
                  inputMode="numeric"
                  invalid={Boolean(fieldErrors.yearEndDay)}
                  onChange={handleFieldChange("yearEndDay")}
                  pattern="[0-9]*"
                  required
                  value={form.yearEndDay}
                />
                <Select
                  aria-label="Year end month"
                  invalid={Boolean(fieldErrors.yearEndMonth)}
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
              {fieldError(
                firstPresentError(fieldErrors, [
                  "yearEndDay",
                  "yearEndMonth",
                ]),
              )}
            </div>
            <div className="ui-field register-card__wide-field">
              <div className="director-editor-heading">
                <span className="ui-field__label" id="register-directors-label">
                  Directors
                </span>
                <Button onClick={handleAddDirector} size="small" type="button">
                  + Add director
                </Button>
              </div>
              <div
                aria-labelledby="register-directors-label"
                className="director-editor"
                role="group"
              >
                {form.directors.map((director, index) => (
                  <div className="director-row" key={index}>
                    <Field label="Name">
                      <Input
                        aria-label={`Director ${index + 1} name`}
                        invalid={Boolean(fieldErrors.directors)}
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
              {fieldError(fieldErrors.directors)}
            </div>
          </div>
        )}

        <div className="register-card__actions">
          {step === 2 ? (
            <Button
              disabled={registerMutation.isPending}
              onClick={() => setStep(1)}
              type="button"
              variant="secondary"
            >
              Back
            </Button>
          ) : null}
          <Button disabled={registerMutation.isPending} type="submit">
            {step === 1
              ? "Continue"
              : registerMutation.isPending
                ? "Creating setup"
                : "Create profile"}
          </Button>
        </div>
      </form>
    </main>
  );
}

function ProgressStep({
  children,
  complete,
  current,
  index,
}: {
  readonly children: ReactNode;
  readonly complete: boolean;
  readonly current: boolean;
  readonly index: number;
}) {
  return (
    <li
      aria-current={current ? "step" : undefined}
      className={[
        "register-progress__step",
        current ? "register-progress__step--current" : "",
        complete ? "register-progress__step--complete" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
      <span>{index}</span>
      <strong>{children}</strong>
    </li>
  );
}

function ProblemPanel({
  closed,
  problem,
}: {
  readonly closed: boolean;
  readonly problem: { detail?: string; title: string };
}) {
  return (
    <div className="problem-alert" role="alert">
      <strong>{problem.title}</strong>
      {problem.detail ? <span>{problem.detail}</span> : null}
      {closed ? <Link to="/login">Return to login</Link> : null}
    </div>
  );
}

function validateIdentityStep(form: RegisterFormState): RegisterFieldErrors {
  const errors: RegisterFieldErrors = {};

  if (!form.email.trim()) {
    errors.email = "Email is required.";
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email.trim())) {
    errors.email = "Use a valid email address.";
  }

  if (!form.name.trim()) {
    errors.name = "Name is required.";
  }

  const passwordError = passwordStrengthError(form.password);
  if (passwordError) {
    errors.password = passwordError;
  }

  return errors;
}

function validateProfileStep(form: RegisterFormState): RegisterFieldErrors {
  const errors: RegisterFieldErrors = {};

  if (!form.tradingName.trim()) {
    errors.tradingName = "Trading name is required.";
  }
  if (!form.legalName.trim()) {
    errors.legalName = "Legal name is required.";
  }
  if (!form.companyNumber.trim()) {
    errors.companyNumber = "Company number is required.";
  }
  if (!form.line1.trim()) {
    errors.line1 = "Registered office line 1 is required.";
  }
  if (!form.locality.trim()) {
    errors.locality = "Registered office town or city is required.";
  }
  if (!form.country.trim()) {
    errors.country = "Registered office country is required.";
  }
  if (!form.incorporationDate) {
    errors.incorporationDate = "Incorporation date is required.";
  }
  if (form.directors.length === 0) {
    errors.directors = "At least one director is required.";
  } else if (form.directors.some((director) => !director.name.trim())) {
    errors.directors = "Director name is required.";
  }

  const yearEndMonth = parseIntegerInput(form.yearEndMonth);
  const yearEndDay = parseIntegerInput(form.yearEndDay);

  if (yearEndMonth === null || yearEndMonth < 1 || yearEndMonth > 12) {
    errors.yearEndMonth = "Choose a year end month.";
  }
  if (yearEndDay === null || yearEndDay < 1) {
    errors.yearEndDay = "Enter a valid year end day.";
  } else if (
    yearEndMonth !== null &&
    yearEndMonth >= 1 &&
    yearEndMonth <= 12 &&
    yearEndDay > daysInMonth(yearEndMonth)
  ) {
    errors.yearEndDay = "Enter a day that exists in the selected month.";
  }

  return errors;
}

function formToRequest(
  form: RegisterFormState,
): IdentityRegisterWithProfileRequest {
  return {
    company_number: form.companyNumber.trim(),
    directors: form.directors.map((director) => ({
      appointed_date: director.appointedDate || undefined,
      is_chair: director.isChair || undefined,
      name: director.name.trim(),
    })),
    email: form.email.trim(),
    incorporation_date: form.incorporationDate,
    legal_name: form.legalName.trim(),
    name: form.name.trim(),
    password: form.password,
    registered_office: {
      country: form.country.trim(),
      line1: form.line1.trim(),
      line2: form.line2.trim(),
      locality: form.locality.trim(),
      postal_code: form.postalCode.trim(),
      region: form.region.trim(),
    },
    trading_name: form.tradingName.trim(),
    year_end_day: requiredIntegerInput(form.yearEndDay),
    year_end_month: requiredIntegerInput(form.yearEndMonth),
  };
}

function emptyDirectorForm(): DirectorFormState {
  return { appointedDate: "", isChair: false, name: "" };
}

function parseIntegerInput(value: string) {
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) {
    return null;
  }

  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

function requiredIntegerInput(value: string) {
  const parsed = parseIntegerInput(value);
  if (parsed === null) {
    throw new Error("Expected a validated integer input.");
  }
  return parsed;
}

function passwordStrengthError(password: string) {
  if (!password) {
    return "Password is required.";
  }
  if (
    password.length < 10 ||
    !/[A-Za-z]/.test(password) ||
    !/[0-9]/.test(password)
  ) {
    return "Use at least 10 characters with letters and numbers.";
  }
  return null;
}

function fieldErrorsFromProblem(problem: Record<string, unknown>) {
  const nextErrors: RegisterFieldErrors = {};
  const errors = problem.errors;
  if (!Array.isArray(errors)) {
    return nextErrors;
  }

  for (const error of errors) {
    if (!isFieldError(error)) {
      continue;
    }

    const field = apiPointerFields[error.pointer];
    if (field && !nextErrors[field]) {
      nextErrors[field] = error.detail;
    }
  }

  return nextErrors;
}

function isFieldError(value: unknown): value is {
  readonly detail: string;
  readonly pointer: string;
} {
  return (
    typeof value === "object" &&
    value !== null &&
    "detail" in value &&
    "pointer" in value &&
    typeof (value as { detail?: unknown }).detail === "string" &&
    typeof (value as { pointer?: unknown }).pointer === "string"
  );
}

function fieldError(message: string | undefined) {
  if (!message) {
    return null;
  }

  return <span className="register-field-error">{message}</span>;
}

function firstPresentError(
  errors: RegisterFieldErrors,
  fields: readonly RegisterField[],
) {
  for (const field of fields) {
    if (errors[field]) {
      return errors[field];
    }
  }
  return undefined;
}

function hasStepOneError(errors: RegisterFieldErrors) {
  return stepOneFields.some((field) => Boolean(errors[field]));
}

function omitFieldError(
  errors: RegisterFieldErrors,
  field: RegisterField,
): RegisterFieldErrors {
  if (!errors[field]) {
    return errors;
  }

  return Object.fromEntries(
    Object.entries(errors).filter(([key]) => key !== field),
  ) as RegisterFieldErrors;
}

function daysInMonth(month: number) {
  if (month === 2) {
    return 29;
  }
  if ([4, 6, 9, 11].includes(month)) {
    return 30;
  }
  return 31;
}
