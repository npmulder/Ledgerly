import { FormEvent, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";

import { isApiError } from "@/api/client";
import { loginIdentity } from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
import { Button, Field, Input } from "@/components";

export function LoginScreen() {
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const loginMutation = useMutation({
    mutationFn: loginIdentity,
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.identity.me() }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.identity.profile(),
        }),
      ]);
      navigate(redirectPathFromState(location.state), { replace: true });
    },
  });
  const problem = isApiError(loginMutation.error)
    ? loginMutation.error.problem
    : null;

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    loginMutation.mutate({ email, password });
  }

  return (
    <main className="login-screen" aria-labelledby="login-title">
      <form className="login-card" onSubmit={handleSubmit}>
        <div className="login-card__header">
          <p className="eyebrow">Authentication</p>
          <h1 id="login-title">Login</h1>
        </div>
        {problem ? (
          <div className="problem-alert" role="alert">
            <strong>{problem.title}</strong>
            {problem.detail ? <span>{problem.detail}</span> : null}
          </div>
        ) : null}
        <div className="login-card__fields">
          <Field label="Email">
            <Input
              autoComplete="email"
              inputMode="email"
              name="email"
              onChange={(event) => setEmail(event.target.value)}
              required
              type="email"
              value={email}
            />
          </Field>
          <Field label="Password">
            <Input
              autoComplete="current-password"
              name="password"
              onChange={(event) => setPassword(event.target.value)}
              required
              type="password"
              value={password}
            />
          </Field>
        </div>
        <Button disabled={loginMutation.isPending} type="submit">
          {loginMutation.isPending ? "Signing in" : "Login"}
        </Button>
      </form>
    </main>
  );
}

function redirectPathFromState(state: unknown) {
  if (!isLocationState(state)) {
    return "/";
  }

  return `${state.from.pathname}${state.from.search}${state.from.hash}`;
}

function isLocationState(state: unknown): state is {
  from: { hash: string; pathname: string; search: string };
} {
  if (!state || typeof state !== "object" || !("from" in state)) {
    return false;
  }

  const from = (state as { from?: unknown }).from;
  return (
    !!from &&
    typeof from === "object" &&
    typeof (from as { pathname?: unknown }).pathname === "string" &&
    typeof (from as { search?: unknown }).search === "string" &&
    typeof (from as { hash?: unknown }).hash === "string"
  );
}
