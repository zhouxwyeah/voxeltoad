import { describe, expect, it, vi } from "vitest";

vi.mock("server-only", () => ({}));

import { mapBackendError } from "./i18n-errors";

describe("mapBackendError", () => {
  it("maps a plain apperr i18n key", () => {
    const mapped = mapBackendError("errors.provider.providerNotFound");
    expect(mapped.key).toBe("provider.providerNotFound");
    expect(mapped.fallback).toBe("errors.provider.providerNotFound");
  });

  it("extracts context from appErrMsg-style messages", () => {
    const mapped = mapBackendError(
      "errors.provider.providerDeleteFailed: provider is referenced by model(s) m1; delete or repoint them first",
    );
    expect(mapped.key).toBe("provider.providerDeleteFailed");
    expect(mapped.fallback).toBe(
      "provider is referenced by model(s) m1; delete or repoint them first",
    );
  });

  it("returns unknown messages as fallback only", () => {
    const mapped = mapBackendError("network error");
    expect(mapped.key).toBeUndefined();
    expect(mapped.fallback).toBe("network error");
  });
});
