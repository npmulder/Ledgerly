import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type IdentityLoginRequest =
  components["schemas"]["IdentityLoginRequest"];
export type IdentityLogoUploadResponse =
  components["schemas"]["IdentityLogoUploadResponse"];
export type IdentityPAT = components["schemas"]["IdentityPAT"];
export type IdentityPATCreateRequest =
  components["schemas"]["IdentityPATCreateRequest"];
export type IdentityPATCreateResponse =
  components["schemas"]["IdentityPATCreateResponse"];
export type IdentityPATListResponse =
  components["schemas"]["IdentityPATListResponse"];
export type IdentityProfile = components["schemas"]["IdentityProfile"];
export type IdentityProfilePatch =
  components["schemas"]["IdentityProfilePatch"];
export type IdentityUser = components["schemas"]["IdentityUser"];

export function getCurrentUser() {
  return apiClient.get("/api/identity/me", { handleUnauthorized: false });
}

export function getIdentityProfile() {
  return apiClient.get("/api/identity/profile");
}

export function getIdentityPATs() {
  return apiClient.get("/api/identity/pats");
}

export function loginIdentity(input: IdentityLoginRequest) {
  return apiClient.post("/api/identity/login", input, {
    handleUnauthorized: false,
  });
}

export function logoutIdentity() {
  return apiClient.post("/api/identity/logout");
}

export function patchIdentityProfile(input: IdentityProfilePatch) {
  return apiClient.patch("/api/identity/profile", input);
}

export function createIdentityPAT(input: IdentityPATCreateRequest) {
  return apiClient.post("/api/identity/pats", input);
}

export function revokeIdentityPAT(id: number) {
  return apiClient.delete(identityPATPath(id));
}

export function replaceIdentityLogo(file: File) {
  const form = new FormData();
  form.append("logo", file, file.name);

  return apiClient.put("/api/identity/logo", form);
}

function identityPATPath(id: number) {
  return `/api/identity/pats/${encodeURIComponent(
    String(id),
  )}` as "/api/identity/pats/{id}";
}
