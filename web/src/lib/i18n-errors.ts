import "server-only";

/**
 * Resolves backend error messages to i18n keys for the errors namespace.
 *
 * The backend (internal/apperr) returns the i18n key directly as the error
 * message, e.g. "errors.tenant.tenantNotFound". The frontend's errors
 * namespace is split into per-domain files (errors/<domain>.json), so the
 * key path "errors.tenant.tenantNotFound" maps to the nested messages object
 * built by request.ts.
 *
 * This helper strips the leading "errors." prefix and returns the remaining
 * dotted path (e.g. "tenant.tenantNotFound") for use with
 * useTranslations("errors") + t(path). Unknown messages (not starting with
 * "errors.") are returned as-is fallback so operators still see something.
 */

export interface MappedError {
  /** Dotted path within the "errors" namespace, or undefined if unmapped. */
  key?: string;
  /** Fallback English text to display if the key is missing/unmapped. */
  fallback: string;
}

/**
 * mapBackendError takes a raw error message string from the backend. If it
 * looks like an apperr i18n key ("errors.<domain>.<key>"), strips the
 * "errors." prefix and returns the rest as the key path. Otherwise returns
 * the message as a fallback only.
 *
 * The backend sometimes appends runtime context after a colon
 * (e.g. "errors.provider.providerDeleteFailed: provider is referenced...").
 * In that case the key is the dotted path before the colon and the fallback
 * is the human-readable context after it, so the UI shows a clean sentence
 * rather than the raw i18n key.
 */
export function mapBackendError(message: string): MappedError {
  const prefix = "errors.";
  if (message.startsWith(prefix)) {
    const rest = message.slice(prefix.length);
    const colonIdx = rest.indexOf(": ");
    const keyPart = colonIdx >= 0 ? rest.slice(0, colonIdx) : rest;
    if (keyPart.includes(".")) {
      return {
        key: keyPart,
        fallback: colonIdx >= 0 ? rest.slice(colonIdx + 2).trim() : message,
      };
    }
  }
  return { fallback: message };
}

/**
 * mapBackendErr works on an unknown error: if it's an Error with a message,
 * maps it; otherwise returns a generic unexpected error.
 */
export function mapBackendErr(err: unknown): MappedError {
  if (err instanceof Error) {
    return mapBackendError(err.message);
  }
  return { fallback: "unexpected error" };
}
