import OpenAI, { type ClientOptions } from "openai";

/**
 * Configuration for the voxeltoad gateway client.
 *
 * The SDK is a thin wrapper over the OpenAI SDK: it speaks the same
 * OpenAI-compatible protocol the gateway exposes, and layers on enterprise
 * concerns (gateway base URL, enterprise API key, usage/quota helpers).
 */
export interface VoxeltoadGatewayOptions {
  /** Enterprise API key issued by the gateway. */
  apiKey: string;
  /**
   * Gateway base URL, e.g. "https://gateway.internal.company.com/v1".
   * Falls back to the VOXELTOAD_BASE_URL env var when omitted.
   */
  baseURL?: string;
  /** Additional options forwarded to the underlying OpenAI client. */
  openai?: Omit<ClientOptions, "apiKey" | "baseURL">;
}

/**
 * VoxeltoadGateway is the enterprise client. Its `chat`, `completions`,
 * `models`, etc. surfaces are inherited from the OpenAI client so call sites
 * look identical to using the OpenAI SDK directly. Enterprise-only
 * capabilities live under dedicated namespaces (e.g. `usage`).
 */
export class VoxeltoadGateway extends OpenAI {
  constructor(options: VoxeltoadGatewayOptions) {
    const baseURL =
      options.baseURL ?? process.env.VOXELTOAD_BASE_URL ?? undefined;

    super({
      apiKey: options.apiKey,
      baseURL,
      ...options.openai,
    });
  }
}

export default VoxeltoadGateway;
