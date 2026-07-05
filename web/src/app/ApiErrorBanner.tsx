import { type PropsWithChildren, useEffect, useState } from "react";

import {
  type ApiErrorNotice,
  setApiErrorReporter,
} from "@/api/errorReporter";

export function ApiErrorBannerProvider({ children }: PropsWithChildren) {
  const [notice, setNotice] = useState<ApiErrorNotice | null>(null);

  useEffect(() => setApiErrorReporter(setNotice), []);

  return (
    <>
      {notice ? (
        <div className="api-error-banner" role="alert">
          <div>
            <strong>{notice.title}</strong>
            {notice.detail ? <span>{notice.detail}</span> : null}
          </div>
          <button type="button" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </div>
      ) : null}
      {children}
    </>
  );
}
