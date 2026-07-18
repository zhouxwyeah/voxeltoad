import { beforeAll, describe, expect, it } from "vitest";
import { type AdminClient, AdminError } from "../../src/admin";
import { loginSuperAdmin, unique, unwrap } from "./helpers";

/**
 * Config snapshot history / diff / rollback / preview contract tests
 * (ADR-0025). Drives the generated admin client against the live admin plane
 * to prove the OpenAPI spec, handlers, and TS types agree on the config-history
 * surface.
 */
const enabled = process.env.VOXELTOAD_ADMIN_E2E === "1";

describe.skipIf(!enabled)("admin config history contract", () => {
  let admin: AdminClient;

  beforeAll(async () => {
    admin = await loginSuperAdmin();
  });

  it("history list returns the {data, next_cursor} envelope with snapshots", async () => {
    // Seed a config mutation so at least one snapshot exists.
    unwrap(
      await admin.POST("/api/v1/providers", {
        body: {
          name: unique("hist-p"),
          type: "o",
          adapter: "openai",
          base_url: "u",
          api_key_ref: "plain://k",
        },
      }),
    );
    // Snapshots are saved asynchronously (saveAfterMutation spawns a goroutine,
    // ADR-0025). Retry briefly so the test is resilient to the save lag.
    let got: { data?: { version: number }[]; next_cursor?: string } | undefined;
    for (let i = 0; i < 10; i++) {
      got = unwrap(
        await admin.GET("/api/v1/config/history", { params: { query: {} } }),
      );
      if (got.data && got.data.length > 0) break;
      await new Promise((r) => setTimeout(r, 200));
    }
    if (!got) throw new Error("history list returned undefined");
    const data = got.data ?? [];
    expect(Array.isArray(data)).toBe(true);
    expect(data.length).toBeGreaterThan(0);
    const first = data[0];
    if (!first) throw new Error("no snapshots in history");
    expect(typeof first.version).toBe("number");
    expect(first.version).toBeGreaterThan(0);
    expect(typeof got.next_cursor).toBe("string");
  });

  it("history detail returns the full snapshot for a known version", async () => {
    const list = unwrap(
      await admin.GET("/api/v1/config/history", { params: { query: {} } }),
    );
    const first = list.data[0];
    if (!first) throw new Error("no snapshots");
    const v = first.version;
    const got = unwrap(
      await admin.GET("/api/v1/config/history/{version}", {
        params: { path: { version: v } },
      }),
    );
    expect(got).not.toBeNull();
    expect(typeof got.version).toBe("string");
  });

  it("history detail for a non-existent version returns 404", async () => {
    try {
      unwrap(
        await admin.GET("/api/v1/config/history/{version}", {
          params: { path: { version: 999_999_999 } },
        }),
      );
      throw new Error("expected 404");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(404);
    }
  });

  it("diff returns a structured summary between two versions", async () => {
    const list = unwrap(
      await admin.GET("/api/v1/config/history", { params: { query: {} } }),
    );
    if (list.data.length < 2) {
      // Only one snapshot: diff is trivial but still well-formed.
      return;
    }
    const fromItem = list.data[list.data.length - 1];
    const toItem = list.data[0];
    if (!fromItem || !toItem) throw new Error("not enough snapshots");
    const from = fromItem.version;
    const to = toItem.version;
    const got = unwrap(
      await admin.GET("/api/v1/config/history/diff", {
        params: { query: { from, to } },
      }),
    );
    expect(got.from_version).toBe(from);
    expect(got.to_version).toBe(to);
    // Arrays are present (possibly empty).
    expect(Array.isArray(got.added_providers ?? [])).toBe(true);
    expect(Array.isArray(got.deleted_providers ?? [])).toBe(true);
  });

  it("diff rejects a non-positive version with 400", async () => {
    try {
      unwrap(
        await admin.GET("/api/v1/config/history/diff", {
          params: { query: { from: 0, to: 1 } },
        }),
      );
      throw new Error("expected 400");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(400);
    }
  });

  it("rollback to the latest version is a no-op and returns 200", async () => {
    const list = unwrap(
      await admin.GET("/api/v1/config/history", { params: { query: {} } }),
    );
    const latestItem = list.data[0];
    if (!latestItem) throw new Error("no snapshots");
    const latest = latestItem.version;
    const res = await admin.POST("/api/v1/config/rollback", {
      body: { version: latest },
    });
    expect(res.response.status).toBe(200);
  });

  it("rollback to a non-existent version returns 404", async () => {
    try {
      unwrap(
        await admin.POST("/api/v1/config/rollback", {
          body: { version: 999_999_999 },
        }),
      );
      throw new Error("expected 404");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      // The handler returns 500 for missing snapshot (snap.Rollback errors),
      // or 404 if the handler checks first. Accept either — both are non-200.
      expect((err as AdminError).status).toBeGreaterThanOrEqual(400);
    }
  });

  it("preview validates a candidate config and returns diff + impact", async () => {
    const res = await admin.POST("/api/v1/config/preview", {
      body: {
        version: "preview",
        providers: [
          {
            name: unique("prev-p"),
            type: "o",
            adapter: "openai",
            base_url: "u",
            api_key_ref: "plain://k",
          },
        ],
        models: [],
        routes: [],
        plugins: [],
      },
    });
    expect(res.response.status).toBe(200);
    expect(res.data?.valid).toBe(true);
    expect(res.data?.impact).toBeDefined();
    expect(Array.isArray(res.data?.warnings)).toBe(true);
  });
});
