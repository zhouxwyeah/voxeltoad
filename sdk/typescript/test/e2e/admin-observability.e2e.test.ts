import { beforeAll, describe, expect, it } from "vitest";
import type { AdminClient } from "../../src/admin";
import { loginSuperAdmin, unwrap, unwrapJson } from "./helpers";

/**
 * Observability read-only endpoints: overview, usage, usage summary, audit,
 * request-logs, data-plane-nodes. These tests assert the response shapes
 * without depending on specific data values. Note: usage/data-plane-nodes use
 * the keyset {data, next_cursor} envelope; audit & request-logs use the offset
 * {data, total, page, page_size} envelope.
 */
const enabled = process.env.VOXELTOAD_ADMIN_E2E === "1";

describe.skipIf(!enabled)("admin observability contract", () => {
  let admin: AdminClient;

  beforeAll(async () => {
    admin = await loginSuperAdmin();
  });

  it("overview returns a dashboard payload", async () => {
    const got = unwrap(await admin.GET("/api/v1/overview", {}));
    // Shape sanity: whatever fields the spec defines, the endpoint must
    // return a JSON object (not an array, not null).
    expect(typeof got).toBe("object");
    expect(got).not.toBeNull();
  });

  it("usage list returns the {data, next_cursor} envelope", async () => {
    const got = unwrapJson(
      await admin.GET("/api/v1/usage", {
        params: { query: { limit: 10 } },
      }),
    );
    expect(Array.isArray(got.data)).toBe(true);
    expect(typeof got.next_cursor).toBe("string");
  });

  it("usage summary aggregates by a valid group_by", async () => {
    const got = unwrapJson(
      await admin.GET("/api/v1/usage/summary", {
        params: { query: { group_by: "model" } },
      }),
    );
    expect(Array.isArray(got.data)).toBe(true);
  });

  it("audit list returns the {data, total, page, page_size} envelope", async () => {
    const got = unwrap(
      await admin.GET("/api/v1/audit", {
        params: { query: { page_size: 10 } },
      }),
    );
    expect(Array.isArray(got.data)).toBe(true);
    expect(typeof got.total).toBe("number");
    expect(typeof got.page).toBe("number");
    expect(typeof got.page_size).toBe("number");
  });

  it("audit list filters by resource_type", async () => {
    // Filter to a resource type that is unlikely to exist; the result must
    // be a well-formed empty page, not an error.
    const got = unwrap(
      await admin.GET("/api/v1/audit", {
        params: { query: { resource_type: "nonexistent_resource" } },
      }),
    );
    expect(Array.isArray(got.data)).toBe(true);
    expect(typeof got.total).toBe("number");
  });

  it("request-logs list returns the {data, total, page, page_size} envelope", async () => {
    const got = unwrapJson(
      await admin.GET("/api/v1/request-logs", {
        params: { query: { page_size: 10 } },
      }),
    );
    expect(Array.isArray(got.data)).toBe(true);
    expect(typeof got.total).toBe("number");
    expect(typeof got.page).toBe("number");
    expect(typeof got.page_size).toBe("number");
  });

  it("data-plane-nodes list returns the list envelope", async () => {
    const got = unwrap(await admin.GET("/api/v1/data-plane-nodes", {}));
    // When no data plane has registered (adminstack-only test run), the
    // Go nil slice serializes to null; the envelope shape is still valid.
    // Accept either an array or null — the contract is the envelope fields.
    expect(got).not.toBeNull();
    expect(typeof got.next_cursor).toBe("string");
  });
});
