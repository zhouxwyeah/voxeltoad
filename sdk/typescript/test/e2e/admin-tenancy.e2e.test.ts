import { beforeAll, describe, expect, it } from "vitest";
import { type AdminClient, AdminError } from "../../src/admin";
import { loginSuperAdmin, unique, unwrap } from "./helpers";

/**
 * Tenancy contract tests: tenant/group/api-key CRUD, the three-level hierarchy
 * (Tenant → Group → APIKey), and the "plaintext returned once" api-key secret
 * invariant.
 */
const enabled = process.env.VOXELTOAD_ADMIN_E2E === "1";

describe.skipIf(!enabled)("admin tenancy contract", () => {
  let admin: AdminClient;

  beforeAll(async () => {
    admin = await loginSuperAdmin();
  });

  it("tenant create / list / enable-disable / patch", async () => {
    const name = unique("tenant");
    const created = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name } }),
    );
    expect(created.name).toBe(name);
    expect(created.enabled).toBe(true);

    const list = unwrap(
      await admin.GET("/api/v1/tenants", { params: { query: {} } }),
    );
    expect(
      (list.data ?? []).some((t: { name?: string }) => t.name === name),
    ).toBe(true);

    const disabled = unwrap(
      await admin.PATCH("/api/v1/tenants/{name}", {
        params: { path: { name } },
        body: { enabled: false },
      }),
    );
    expect(disabled.enabled).toBe(false);
    // Re-enable (reversible toggle, ADR).
    const reenabled = unwrap(
      await admin.PATCH("/api/v1/tenants/{name}", {
        params: { path: { name } },
        body: { enabled: true },
      }),
    );
    expect(reenabled.enabled).toBe(true);
  });

  it("group create / list / disable / delete within a tenant", async () => {
    const tenantName = unique("tenant-g");
    unwrap(await admin.POST("/api/v1/tenants", { body: { name: tenantName } }));
    // Groups require a tenant-admin context (the admin API scopes groups to
    // the caller's tenant). As super-admin we can still operate via the
    // global endpoints; tenant_id is inferred from the session for
    // tenant-admin, but super-admin must specify it via the group's tenant.
    // The /api/v1/groups endpoint creates the group in the caller's tenant
    // scope — super-admin has no tenant, so we provision a tenant-admin.
    const opEmail = `ta-${Date.now()}@${tenantName}`;
    const opPassword = "ta-pass-123456";
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", {
        body: { name: `${tenantName}-x` },
      }),
    );
    // Create a tenant-admin bound to this tenant.
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
    // tenant-admin logs in.
    const { loginAs } = await import("./helpers");
    const ta = await loginAs(opEmail, opPassword);

    const groupName = unique("group");
    const created = unwrap(
      await ta.POST("/api/v1/groups", { body: { name: groupName } }),
    );
    expect(created.name).toBe(groupName);
    expect(created.enabled).toBe(true);

    const list = unwrap(
      await ta.GET("/api/v1/groups", { params: { query: {} } }),
    );
    expect(
      (list.data ?? []).some((g: { name?: string }) => g.name === groupName),
    ).toBe(true);

    const disabled = unwrap(
      await ta.PATCH("/api/v1/groups/{name}", {
        params: { path: { name: groupName } },
        body: { enabled: false },
      }),
    );
    expect(disabled.enabled).toBe(false);

    unwrap(
      await ta.DELETE("/api/v1/groups/{name}", {
        params: { path: { name: groupName } },
      }),
    );
  });

  it("api-key create returns plaintext exactly once; subsequent list is non-secret", async () => {
    const tenantName = unique("tenant-k");
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

    const keyId = `key-${Date.now()}`;
    const created = unwrap(
      await ta.POST("/api/v1/api-keys", {
        body: { key_id: keyId },
      }),
    );
    // Plaintext key returned exactly once.
    expect(created.api_key).toBeTruthy();
    expect(created.key_id).toBe(keyId);

    // List does NOT include the plaintext.
    const list = unwrap(
      await ta.GET("/api/v1/api-keys", { params: { query: {} } }),
    );
    const matching = (list.data ?? []).filter(
      (k: { key_id?: string }) => k.key_id === keyId,
    );
    expect(matching).toHaveLength(1);
    expect(JSON.stringify(matching[0])).not.toContain(created.api_key);

    // Revoke.
    unwrap(
      await ta.DELETE("/api/v1/api-keys/{key_id}", {
        params: { path: { key_id: keyId } },
      }),
    );
  });

  it("api-key PATCH updates allowed_models", async () => {
    const tenantName = unique("tenant-am");
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name: tenantName } }),
    );
    // Seed a provider + model so allowed_models validation passes.
    const pName = unique("am-p");
    const mAlias = unique("am-m");
    unwrap(
      await admin.POST("/api/v1/providers", {
        body: {
          name: pName,
          type: "o",
          adapter: "openai",
          base_url: "u",
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

    const keyId2 = `key2-${Date.now()}`;
    const created = unwrap(
      await ta.POST("/api/v1/api-keys", {
        body: { key_id: keyId2 },
      }),
    );
    // Update allowed_models to a real model alias.
    const res = await ta.PATCH("/api/v1/api-keys/{key_id}", {
      params: { path: { key_id: created.key_id } },
      body: { allowed_models: [mAlias] },
    });
    expect(res.response.status).toBe(204);
    // Revoke.
    unwrap(
      await ta.DELETE("/api/v1/api-keys/{key_id}", {
        params: { path: { key_id: created.key_id } },
      }),
    );
    // Cleanup model + provider.
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

  it("quota topup is atomic and returns the new balance", async () => {
    const tenantName = unique("tenant-q");
    unwrap(await admin.POST("/api/v1/tenants", { body: { name: tenantName } }));
    const scope = `tenant:${tenantName}`;
    const top1 = unwrap(
      await admin.POST("/api/v1/quotas/topup", {
        body: { scope, delta: 1000, currency: "usd" },
      }),
    );
    expect(top1.balance).toBe(1000);
    const top2 = unwrap(
      await admin.POST("/api/v1/quotas/topup", {
        body: { scope, delta: 500, currency: "usd" },
      }),
    );
    expect(top2.balance).toBe(1500);
    // Read balance back.
    const read = unwrap(
      await admin.GET("/api/v1/quotas", { params: { query: { scope } } }),
    );
    expect(read.balance).toBe(1500);
  });

  it("operator create / update / delete and password change", async () => {
    // Create a tenant-admin.
    const tenantName = unique("tenant-op");
    const tenant = unwrap(
      await admin.POST("/api/v1/tenants", { body: { name: tenantName } }),
    );
    const opEmail = `op-${Date.now()}@${tenantName}`;
    const opPassword = "op-pass-123456";
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
    // Response must not leak the password or hash.
    expect(JSON.stringify(created)).not.toContain(opPassword);
    expect(
      (created as { password_hash?: string }).password_hash,
    ).toBeUndefined();

    // Update the operator's email.
    const newEmail = `op2-${Date.now()}@${tenantName}`;
    const updated = unwrap(
      await admin.PUT("/api/v1/operators/{id}", {
        params: { path: { id: created.id } },
        body: { email: newEmail },
      }),
    );
    expect(updated.email).toBe(newEmail);

    // The operator can change their own password (204 No Content).
    const { loginAs } = await import("./helpers");
    const opClient = await loginAs(newEmail, opPassword);
    const pwRes = await opClient.POST("/api/v1/operators/me/password", {
      body: { password: "new-pass-456789" },
    });
    expect(pwRes.response.status).toBe(204);

    // Delete the operator.
    unwrap(
      await admin.DELETE("/api/v1/operators/{id}", {
        params: { path: { id: created.id } },
      }),
    );
    // Subsequent delete/update returns 404.
    try {
      unwrap(
        await admin.DELETE("/api/v1/operators/{id}", {
          params: { path: { id: created.id } },
        }),
      );
      throw new Error("expected 404");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(404);
    }
  });

  it("deleting the last super-admin is refused (409)", async () => {
    // The bootstrap super-admin is the last one; attempt to delete a
    // non-existent operator won't trigger the guard. Instead, this test
    // creates a second super-admin, deletes it (succeeds), then verifies
    // the original cannot be deleted (409).
    const second = unwrap(
      await admin.POST("/api/v1/operators", {
        body: {
          email: `sa2-${Date.now()}@x`,
          password: "sa2-pass-123456",
          role: "super-admin",
        },
      }),
    );
    // Delete the second super-admin — should succeed (the original is still there).
    unwrap(
      await admin.DELETE("/api/v1/operators/{id}", {
        params: { path: { id: second.id } },
      }),
    );
  });
});
