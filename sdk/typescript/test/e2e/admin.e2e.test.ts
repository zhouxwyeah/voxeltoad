import { beforeAll, describe, expect, it } from "vitest";
import { AdminError, createAdminClient, unwrap } from "../../src/admin";

/**
 * Cross-language contract test (阶段4, ADR-0019). It drives the GENERATED admin
 * client — whose types come from docs/openapi/admin.yaml — against a running Go
 * admin server, exercising the operator flow: login → create tenant → top up
 * quota → read usage/audit. Because the client is generated from the spec, this
 * proves the spec, the Go implementation, and the TS types all agree.
 *
 * Opt-in (like chat.e2e.test.ts) so `npm test` / `make ci` stay hermetic. To
 * run against a live admin plane:
 *
 *   # bring up the admin server + a bootstrapped super-admin, then:
 *   VOXELTOAD_ADMIN_E2E=1 \
 *   VOXELTOAD_ADMIN_BASE_URL=http://localhost:8090 \
 *   VOXELTOAD_ADMIN_EMAIL=root@local VOXELTOAD_ADMIN_PASSWORD=... \
 *   npm run test:e2e
 *
 * Even when skipped, this file is type-checked at build: the generated client's
 * request/response types are validated against these call sites, so a spec
 * change that breaks a call site fails `npm run typecheck` in CI.
 */
const enabled = process.env.VOXELTOAD_ADMIN_E2E === "1";
const baseUrl = process.env.VOXELTOAD_ADMIN_BASE_URL ?? "http://localhost:8090";
const email = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@local";
const password = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "change-me";

describe.skipIf(!enabled)("admin API contract (generated client)", () => {
  let token: string;
  const tenantName = `contract-${Date.now()}`;

  beforeAll(async () => {
    const anon = createAdminClient({ baseUrl });
    const { data, error } = await anon.POST("/auth/login", {
      body: { email, password },
    });
    expect(error).toBeUndefined();
    expect(data?.token).toBeTruthy();
    token = data?.token ?? "";
  });

  it("creates a tenant (via unwrap helper)", async () => {
    const admin = createAdminClient({ baseUrl, token });
    // unwrap() returns typed data or throws AdminError — the ergonomic path the
    // UI uses instead of hand-checking { data, error } everywhere.
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name: tenantName } }),
    );
    expect(tenant.name).toBe(tenantName);
    expect(typeof tenant.id).toBe("number");
  });

  it("tops up a quota and reads the new balance", async () => {
    const admin = createAdminClient({ baseUrl, token });
    const scope = `tenant:${tenantName}`;

    const topup = await admin.POST("/api/v1/quotas/topup", {
      body: { scope, delta: 5000, currency: "usd" },
    });
    expect(topup.response.status).toBe(200);
    expect(topup.data?.balance).toBe(5000);

    const read = await admin.GET("/api/v1/quotas", {
      params: { query: { scope } },
    });
    expect(read.response.status).toBe(200);
    expect(read.data?.balance).toBe(5000);
  });

  it("creates a tenant-admin operator that can then log in", async () => {
    const admin = createAdminClient({ baseUrl, token });
    // The UI-critical path: a super-admin provisions a tenant-admin account.
    const opTenant = `${tenantName}-ops`;
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name: opTenant } }),
    );
    const opEmail = `ta-${Date.now()}@${opTenant}`;
    const opPassword = "ta-pass-123456";

    const created = unwrap(
      await admin.POST("/api/v1/operators", {
        body: {
          email: opEmail,
          password: opPassword,
          role: "tenant-admin",
          tenant_id: tenant.id,
        },
      }),
    );
    expect(created.email).toBe(opEmail);
    expect(created.role).toBe("tenant-admin");
    // The response must not leak the secret.
    expect((created as Record<string, unknown>).password).toBeUndefined();
    expect((created as Record<string, unknown>).password_hash).toBeUndefined();

    // The freshly-created account authenticates.
    const anon = createAdminClient({ baseUrl });
    const loginRes = await anon.POST("/auth/login", {
      body: { email: opEmail, password: opPassword },
    });
    expect(loginRes.response.status).toBe(200);
    expect(loginRes.data?.token).toBeTruthy();
  });

  it("lists usage with the {data,next_cursor} envelope", async () => {
    const admin = createAdminClient({ baseUrl, token });
    const { data, response } = await admin.GET("/api/v1/usage", {
      params: { query: { limit: 10 } },
    });
    expect(response.status).toBe(200);
    // /usage advertises both JSON and text/csv on 200, so `data` is a
    // `string | UsageList` union; this call requests JSON, so drop the CSV arm.
    const usage = data as Exclude<typeof data, string> | undefined;
    expect(Array.isArray(usage?.data)).toBe(true);
    expect(typeof usage?.next_cursor).toBe("string");
  });

  it("reads the audit feed", async () => {
    const admin = createAdminClient({ baseUrl, token });
    const { data, response } = await admin.GET("/api/v1/audit", {
      params: { query: { page_size: 10 } },
    });
    expect(response.status).toBe(200);
    expect(Array.isArray(data?.data)).toBe(true);
  });

  it("deleting a referenced provider returns a typed 409", async () => {
    const admin = createAdminClient({ baseUrl, token });
    const pName = `ref-prot-${Date.now()}`;
    const mAlias = `ref-model-${Date.now()}`;

    // Create provider + model referencing it.
    unwrap(
      await admin.POST("/api/v1/providers", {
        body: {
          name: pName,
          type: "o",
          adapter: "openai",
          base_url: "https://api.example.com",
          api_key_ref: "plain://k",
        },
      }),
    );
    unwrap(
      await admin.POST("/api/v1/models", {
        body: {
          alias: mAlias,
          upstreams: [{ provider: pName, upstream_model: "gpt-4o" }],
        },
      }),
    );

    // Deleting the provider must fail with a typed 409.
    try {
      unwrap(
        await admin.DELETE("/api/v1/providers/{name}", {
          params: { path: { name: pName } },
        }),
      );
      throw new Error("expected unwrap to throw on a 409");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(409);
    }

    // After removing the model, delete succeeds.
    unwrap(
      await admin.DELETE("/api/v1/models/{alias}", {
        params: { path: { alias: mAlias } },
      }),
    );
    unwrap(
      await admin.DELETE("/api/v1/providers/{name}", {
        params: { path: { name: pName } },
      }),
    );
  });

  it("surfaces API errors as a typed AdminError (via unwrap)", async () => {
    const admin = createAdminClient({ baseUrl, token });
    // An unknown group_by is rejected 400 with the {error:{message,type}}
    // envelope; unwrap turns that into an AdminError the UI can branch on.
    try {
      unwrap(
        await admin.GET("/api/v1/usage/summary", {
          params: { query: { group_by: "bogus" as never } },
        }),
      );
      throw new Error("expected unwrap to throw on a 400");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(400);
      expect((err as AdminError).type).toBe("invalid_body");
    }
  });
});
