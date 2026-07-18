import { beforeAll, describe, expect, it } from "vitest";
import { AdminError, createAdminClient } from "../../src/admin";
import { loginSuperAdmin, unique, unwrap } from "./helpers";

/**
 * RBAC contract tests: super-admin vs tenant-admin enforcement, and the
 * authn boundary (401 without a token). These prove the structural isolation
 * in internal/store/tenant.go holds end-to-end through the HTTP API.
 */
const enabled = process.env.VOXELTOAD_ADMIN_E2E === "1";

describe.skipIf(!enabled)("admin RBAC contract", () => {
  let admin: ReturnType<typeof createAdminClient>;

  beforeAll(async () => {
    admin = await loginSuperAdmin();
  });

  it("requests without a token are rejected with 401", async () => {
    // Use a client with no token.
    const anon = createAdminClient({
      baseUrl: process.env.VOXELTOAD_ADMIN_BASE_URL ?? "http://localhost:8090",
    });
    const res = await anon.GET("/api/v1/providers", { params: { query: {} } });
    expect(res.response.status).toBe(401);
    expect(res.error?.error?.type).toBeTruthy();
  });

  it("tenant-admin is forbidden from global config endpoints (403)", async () => {
    const tenantName = unique("rbac-tenant");
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name: tenantName } }),
    );
    const opEmail = `ta-${Date.now()}@${tenantName}`;
    const opPassword = "ta-pass-123456";
    unwrap(
      await admin.POST("/api/v1/operators", {
        body: {
          email: opEmail,
          password: opPassword,
          role: "tenant-admin",
          tenant_id: tenant.id,
        },
      }),
    );
    const { loginAs } = await import("./helpers");
    const ta = await loginAs(opEmail, opPassword);

    // tenant-admin CAN read models: they are global shared config, and the
    // API-key form needs model aliases (mirrors api_key_dbtest_test.go's
    // TestAPIKey_ModelsRead_AsTenantAdmin → 200). This is the deliberate
    // read-open/write-closed carve-out.
    const modelsRes = await ta.GET("/api/v1/models", { params: { query: {} } });
    expect(modelsRes.response.status).toBe(200);

    // tenant-admin cannot access the OTHER global config endpoints. These are
    // platform-level config/tenancy, super-admin only (migration 00011 seeds
    // tenant-admin WITHOUT provider/route/plugin/tenant/operator read perms).
    for (const [path, method] of [
      ["/api/v1/providers", "GET"],
      ["/api/v1/routes", "GET"],
      ["/api/v1/plugins", "GET"],
      ["/api/v1/operators", "GET"],
      ["/api/v1/tenants", "GET"],
    ] as const) {
      const res = await ta.GET(path as "/api/v1/providers", {
        params: { query: {} },
      });
      expect(res.response.status).toBe(403);
      // Sanity: the method is GET (compile-time check the typed client).
      expect(method).toBe("GET");
    }

    // tenant-admin cannot topup quota (super-admin only).
    try {
      unwrap(
        await ta.POST("/api/v1/quotas/topup", {
          body: { scope: `tenant:${tenantName}`, delta: 100, currency: "usd" },
        }),
      );
      throw new Error("expected 403");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(403);
    }
  });

  it("tenant-admin can read own tenant's groups and api-keys", async () => {
    const tenantName = unique("rbac-own");
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name: tenantName } }),
    );
    const opEmail = `ta-${Date.now()}@${tenantName}`;
    const opPassword = "ta-pass-123456";
    unwrap(
      await admin.POST("/api/v1/operators", {
        body: {
          email: opEmail,
          password: opPassword,
          role: "tenant-admin",
          tenant_id: tenant.id,
        },
      }),
    );
    const { loginAs } = await import("./helpers");
    const ta = await loginAs(opEmail, opPassword);

    // Groups and api-keys are scoped to the caller's tenant → 200.
    const groups = await ta.GET("/api/v1/groups", { params: { query: {} } });
    expect(groups.response.status).toBe(200);
    expect(Array.isArray(groups.data?.data)).toBe(true);

    const keys = await ta.GET("/api/v1/api-keys", { params: { query: {} } });
    expect(keys.response.status).toBe(200);
    expect(Array.isArray(keys.data?.data)).toBe(true);
  });

  it("/me returns the authenticated operator's identity", async () => {
    const me = unwrap(await admin.GET("/api/v1/me", {}));
    expect(me.email).toBeTruthy();
    expect(me.role).toBe("super-admin");
  });
});
