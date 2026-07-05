import { isApiError } from "@/api/client";

export type ApiErrorNotice = {
  detail?: string;
  title: string;
};

type ApiErrorReporter = (notice: ApiErrorNotice) => void;

let activeReporter: ApiErrorReporter | undefined;

export function setApiErrorReporter(reporter: ApiErrorReporter) {
  activeReporter = reporter;

  return () => {
    if (activeReporter === reporter) {
      activeReporter = undefined;
    }
  };
}

export function notifyUnexpectedApiError(error: unknown) {
  if (!shouldReport(error)) {
    return;
  }

  activeReporter?.(noticeFromError(error));
}

function shouldReport(error: unknown) {
  if (!isApiError(error)) {
    return true;
  }

  if (error.status === 401) {
    return false;
  }

  return !error.isClientError;
}

function noticeFromError(error: unknown): ApiErrorNotice {
  if (isApiError(error)) {
    return {
      detail: error.problem.detail ?? `HTTP ${error.status}`,
      title: error.problem.title || "API request failed",
    };
  }

  if (error instanceof Error) {
    return {
      detail: error.message,
      title: "Unexpected error",
    };
  }

  return {
    title: "Unexpected error",
  };
}
