import type { components, paths } from "@/api/generated/schema";

type SuccessStatus =
  | 200
  | 201
  | 202
  | 203
  | 204
  | 205
  | 206
  | 207
  | 208
  | 226
  | "200"
  | "201"
  | "202"
  | "203"
  | "204"
  | "205"
  | "206"
  | "207"
  | "208"
  | "226";

type PathWithMethod<TMethod extends string> = {
  [Path in keyof paths]: paths[Path] extends Record<TMethod, unknown>
    ? Path
    : never;
}[keyof paths] &
  string;

type OperationFor<
  Path extends PathWithMethod<TMethod>,
  TMethod extends string,
> = paths[Path] extends Record<TMethod, infer Operation> ? Operation : never;

type ResponsesFor<Operation> = Operation extends { responses: infer Responses }
  ? Responses
  : never;

type ResponseByStatus<Responses, Status> = Responses extends object
  ? Responses[Extract<keyof Responses, Status>]
  : never;

type JsonBody<Response> = Response extends { content: infer Content }
  ? Content extends { "application/json": infer Body }
    ? Body
    : Content extends { "application/problem+json": infer Body }
      ? Body
      : never
  : undefined;

type SuccessBody<Operation> = JsonBody<
  ResponseByStatus<ResponsesFor<Operation>, SuccessStatus>
>;

type QueryParameters<Operation> = Operation extends {
  parameters: { query?: infer Query };
}
  ? Query
  : never;

type JsonRequestBody<Operation> = Operation extends {
  requestBody: { content: { "application/json": infer Body } };
}
  ? Body
  : never;

export type ProblemDetails = components["schemas"]["Problem"] &
  Record<string, unknown>;

export type ApiGetPath = PathWithMethod<"get">;
export type ApiPatchPath = PathWithMethod<"patch">;
export type ApiPostPath = PathWithMethod<"post">;
export type ApiPutPath = PathWithMethod<"put">;

export type ApiRequestOptions<Operation> = {
  handleUnauthorized?: boolean;
  headers?: HeadersInit;
  query?: QueryParameters<Operation>;
  signal?: AbortSignal;
};

export type FetchLike = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export type ApiClientOptions = {
  baseUrl?: string;
  fetchImpl?: FetchLike;
  onUnauthorized?: (error: ApiError) => void;
};

export class ApiError<
  TProblem extends ProblemDetails = ProblemDetails,
> extends Error {
  name = "ApiError";
  readonly problem: TProblem;
  readonly response: Response;
  readonly status: number;

  constructor(response: Response, problem: TProblem) {
    super(
      problem.detail ? `${problem.title}: ${problem.detail}` : problem.title,
    );
    this.problem = problem;
    this.response = response;
    this.status = response.status;
  }

  get isClientError() {
    return this.status >= 400 && this.status < 500;
  }
}

export class ApiClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: FetchLike;
  private readonly onUnauthorized: (error: ApiError) => void;

  constructor(options: ApiClientOptions = {}) {
    this.baseUrl = options.baseUrl ?? "";
    this.fetchImpl =
      options.fetchImpl ?? ((input, init) => globalThis.fetch(input, init));
    this.onUnauthorized = options.onUnauthorized ?? redirectToLogin;
  }

  async get<Path extends ApiGetPath>(
    path: Path,
    options: ApiRequestOptions<OperationFor<Path, "get">> = {},
  ): Promise<SuccessBody<OperationFor<Path, "get">>> {
    return this.request<Path, "get">(path, "GET", options);
  }

  async patch<Path extends ApiPatchPath>(
    path: Path,
    body?: JsonRequestBody<OperationFor<Path, "patch">>,
    options: ApiRequestOptions<OperationFor<Path, "patch">> = {},
  ): Promise<SuccessBody<OperationFor<Path, "patch">>> {
    return this.request<Path, "patch">(path, "PATCH", options, body);
  }

  async post<Path extends ApiPostPath>(
    path: Path,
    body?: JsonRequestBody<OperationFor<Path, "post">>,
    options: ApiRequestOptions<OperationFor<Path, "post">> = {},
  ): Promise<SuccessBody<OperationFor<Path, "post">>> {
    return this.request<Path, "post">(path, "POST", options, body);
  }

  async put<Path extends ApiPutPath>(
    path: Path,
    body?: FormData | JsonRequestBody<OperationFor<Path, "put">>,
    options: ApiRequestOptions<OperationFor<Path, "put">> = {},
  ): Promise<SuccessBody<OperationFor<Path, "put">>> {
    return this.request<Path, "put">(path, "PUT", options, body);
  }

  private async request<
    Path extends PathWithMethod<TMethod>,
    TMethod extends string,
  >(
    path: Path,
    method: string,
    options: ApiRequestOptions<OperationFor<Path, TMethod>>,
    body?: FormData | JsonRequestBody<OperationFor<Path, TMethod>>,
  ): Promise<SuccessBody<OperationFor<Path, TMethod>>> {
    const response = await this.fetchImpl(
      buildUrl(
        this.baseUrl,
        path,
        options.query as Record<string, unknown> | undefined,
      ),
      {
        body: buildBody(body),
        credentials: "same-origin",
        headers: buildHeaders(options.headers, body),
        method,
        signal: options.signal,
      },
    );

    if (response.ok) {
      return (await readJson(response)) as SuccessBody<
        OperationFor<Path, TMethod>
      >;
    }

    const error = new ApiError(response, await readProblem(response));
    if (error.status === 401 && options.handleUnauthorized !== false) {
      this.onUnauthorized(error);
    }
    throw error;
  }
}

export const apiClient = new ApiClient();

export function apiGet<Path extends ApiGetPath>(
  path: Path,
  options?: ApiRequestOptions<OperationFor<Path, "get">>,
) {
  return apiClient.get(path, options);
}

export function isApiError(value: unknown): value is ApiError {
  return value instanceof ApiError;
}

function buildUrl(
  baseUrl: string,
  path: string,
  query: Record<string, unknown> | undefined,
) {
  const url = baseUrl ? `${baseUrl.replace(/\/$/, "")}${path}` : path;
  const params = buildSearchParams(query);
  const queryString = params.toString();

  if (!queryString) {
    return url;
  }

  return `${url}${url.includes("?") ? "&" : "?"}${queryString}`;
}

function buildSearchParams(query: Record<string, unknown> | undefined) {
  const params = new URLSearchParams();

  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === undefined || value === null) {
      continue;
    }

    if (Array.isArray(value)) {
      for (const item of value) {
        params.append(key, String(item));
      }
      continue;
    }

    params.set(key, String(value));
  }

  return params;
}

function buildBody(body: unknown) {
  if (body === undefined) {
    return undefined;
  }

  if (body instanceof FormData) {
    return body;
  }

  return JSON.stringify(body);
}

function buildHeaders(headers: HeadersInit | undefined, body: unknown) {
  const nextHeaders = new Headers({
    Accept: "application/json, application/problem+json",
  });

  if (body !== undefined && !(body instanceof FormData)) {
    nextHeaders.set("Content-Type", "application/json");
  }

  new Headers(headers).forEach((value, key) => {
    nextHeaders.set(key, value);
  });

  return nextHeaders;
}

async function readJson(response: Response) {
  if (response.status === 204) {
    return undefined;
  }

  const text = await response.text();
  if (text.length === 0) {
    return undefined;
  }

  return JSON.parse(text) as unknown;
}

async function readProblem(response: Response): Promise<ProblemDetails> {
  const body = await readJsonSafely(response);
  if (isProblemDetails(body)) {
    return body;
  }

  return {
    status: response.status,
    title: response.statusText || "Request failed",
    type: "about:blank",
  };
}

async function readJsonSafely(response: Response) {
  try {
    return await readJson(response);
  } catch {
    return undefined;
  }
}

function isProblemDetails(value: unknown): value is ProblemDetails {
  if (!isRecord(value)) {
    return false;
  }

  return (
    typeof value.type === "string" &&
    typeof value.title === "string" &&
    typeof value.status === "number"
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function redirectToLogin(error: ApiError) {
  void error;

  if (typeof window === "undefined" || window.location.pathname === "/login") {
    return;
  }

  const returnTo = `${window.location.pathname}${window.location.search}`;
  window.location.assign(`/login?returnTo=${encodeURIComponent(returnTo)}`);
}
