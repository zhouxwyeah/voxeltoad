"use server";

import { serverAdminClient } from "@/lib/admin";
import { onAuthExpired } from "@/lib/errors";
import { unwrap } from "@voxeltoad/gateway-sdk/admin";
import { revalidatePath } from "next/cache";

/**
 * Rollback the live config to a specific snapshot version (ADR-0025). The
 * handler replaces all config tables and bumps config_generation; the data
 * plane picks up the new generation on its next poll. The rollback itself
 * produces a new snapshot version, so it is auditable via history.
 */
export async function rollbackAction(version: number): Promise<void> {
  try {
    const client = await serverAdminClient();
    unwrap(
      await client.POST("/api/v1/config/rollback", { body: { version } }),
    );
  } catch (err) {
    await onAuthExpired(err);
  }
  // Refresh the history list so the new snapshot row appears.
  revalidatePath("/config/history");
}

/** Structured diff between two snapshot versions (resource-name lists). */
export type ConfigDiffResult = {
  added_providers?: string[];
  deleted_providers?: string[];
  added_models?: string[];
  deleted_models?: string[];
  added_routes?: string[];
  deleted_routes?: string[];
  added_plugins?: string[];
  deleted_plugins?: string[];
};

/**
 * Structured diff between two snapshot versions. Server-side fetch: the
 * browser never calls the admin API directly (design/frontend.md §2 — no
 * client-side fetch).
 */
export async function diffAction(
  from: number,
  to: number,
): Promise<ConfigDiffResult> {
  try {
    const client = await serverAdminClient();
    const res = unwrap(
      await client.GET("/api/v1/config/history/diff", {
        params: { query: { from, to } },
      }),
    );
    return res;
  } catch (err) {
    await onAuthExpired(err);
    throw err;
  }
}

/**
 * Fetch a single snapshot's full content (used to dry-run preview a rollback
 * candidate). Server-side fetch, same reasoning as diffAction.
 */
export async function snapshotAction(
  version: number,
): Promise<Record<string, unknown>> {
  try {
    const client = await serverAdminClient();
    const res = unwrap(
      await client.GET("/api/v1/config/history/{version}", {
        params: { path: { version } },
      }),
    );
    return res as Record<string, unknown>;
  } catch (err) {
    await onAuthExpired(err);
    throw err;
  }
}

/**
 * Dry-run preview: validate a candidate config and diff against current.
 * Returns the validation result + diff + impact summary.
 */
export async function previewAction(body: Record<string, unknown>): Promise<{
  valid: boolean;
  diff: Record<string, string[]>;
  impact: { new_resources?: number; deleted_resources?: number; changed_resources?: number };
  warnings: string[];
}> {
  try {
    const client = await serverAdminClient();
    const res = unwrap(
      await client.POST("/api/v1/config/preview", {
        body: body as never,
      }),
    );
    return res as {
      valid: boolean;
      diff: Record<string, string[]>;
      impact: { new_resources?: number; deleted_resources?: number; changed_resources?: number };
      warnings: string[];
    };
  } catch (err) {
    await onAuthExpired(err);
    throw err;
  }
}
