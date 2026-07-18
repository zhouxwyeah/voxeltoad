import { beforeAll, describe, expect, it } from "vitest";
import { type AdminClient, AdminError } from "../../src/admin";
import { loginSuperAdmin, unique, unwrap } from "./helpers";

/**
 * Provider / Model / Route / Plugin CRUD contract tests. Drives the generated
 * admin client against a live admin plane, proving the OpenAPI spec, the Go
 * handlers, and the TS types all agree on the config-resource surface.
 *
 * P0-1 (ADR-0031): provider responses mask api_key_ref — these tests assert the
 * masked forms so a regression that leaks the ref fails loudly.
 */
const enabled = process.env.VOXELTOAD_ADMIN_E2E === "1";

describe.skipIf(!enabled)("admin config CRUD contract", () => {
  let admin: AdminClient;

  beforeAll(async () => {
    admin = await loginSuperAdmin();
  });

  it("provider POST returns a masked api_key_ref (env://)", async () => {
    const name = unique("prov");
    const got = unwrap(
      await admin.POST("/api/v1/providers", {
        body: {
          name,
          type: "openai",
          adapter: "openai",
          base_url: "https://api.openai.com/v1",
          api_key_ref: "env://OPENAI_KEY",
        },
      }),
    );
    expect(got.api_key_ref).toBe("env://***");
    expect(got.name).toBe(name);

    const list = unwrap(
      await admin.GET("/api/v1/providers", { params: { query: {} } }),
    );
    expect(Array.isArray(list.data)).toBe(true);
    const matching = (list.data ?? []).filter(
      (p: { name?: string }) => p.name === name,
    );
    expect(matching).toHaveLength(1);
    expect((matching[0] as { api_key_ref?: string }).api_key_ref).toBe(
      "env://***",
    );
  });

  it("provider POST with api_key encrypts and returns db://provider/<name>", async () => {
    const name = unique("prov-enc");
    const got = unwrap(
      await admin.POST("/api/v1/providers", {
        body: {
          name,
          type: "openai",
          adapter: "openai",
          base_url: "https://api.openai.com/v1",
          api_key: "sk-test-secret-12345",
        },
      }),
    );
    expect(got.api_key_ref).toBe(`db://provider/${name}`);
    // The plaintext key is never returned.
    expect(JSON.stringify(got)).not.toContain("sk-test-secret-12345");
  });

  it("provider PATCH /credential rotates the stored credential", async () => {
    const name = unique("prov-rot");
    unwrap(
      await admin.POST("/api/v1/providers", {
        body: {
          name,
          type: "openai",
          adapter: "openai",
          base_url: "https://api.openai.com/v1",
          api_key_ref: "env://OPENAI_KEY",
        },
      }),
    );
    const res = await admin.PATCH("/api/v1/providers/{name}/credential", {
      params: { path: { name } },
      body: { api_key: "sk-rotated-67890" },
    });
    expect(res.response.status).toBe(200);
    expect(res.data?.api_key_ref).toBe(`db://provider/${name}`);
    expect(JSON.stringify(res.data)).not.toContain("sk-rotated-67890");
  });

  it("provider DELETE with model reference returns a typed 409", async () => {
    const pName = unique("prov-ref");
    const mAlias = unique("model-ref");
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
    // Cleanup: remove model then provider.
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

  it("model POST with unknown upstream provider returns 400", async () => {
    try {
      unwrap(
        await admin.POST("/api/v1/models", {
          body: {
            alias: unique("bad-model"),
            upstreams: [
              { provider: "ghost-provider", upstream_model: "gpt-4o" },
            ],
          },
        }),
      );
      throw new Error("expected unwrap to throw on a 400");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(400);
    }
  });

  it("model + route CRUD round-trip", async () => {
    const pName = unique("p");
    const mAlias = unique("m");
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
    // Create model.
    unwrap(
      await admin.POST("/api/v1/models", {
        body: {
          alias: mAlias,
          upstreams: [{ provider: pName, upstream_model: "gpt-4o" }],
        },
      }),
    );
    // Create route referencing the model.
    unwrap(
      await admin.POST("/api/v1/routes", {
        body: {
          model_alias: mAlias,
          strategy: "priority",
          providers: [{ name: pName }],
        },
      }),
    );
    // List routes and find ours.
    const list = unwrap(
      await admin.GET("/api/v1/routes", { params: { query: {} } }),
    );
    const matching = (list.data ?? []).filter(
      (r: { model_alias?: string }) => r.model_alias === mAlias,
    );
    expect(matching).toHaveLength(1);
    expect((matching[0] as { strategy?: string }).strategy).toBe("priority");
    // Delete route → model → provider (order matters for FK).
    unwrap(
      await admin.DELETE("/api/v1/routes/{alias}", {
        params: { path: { alias: mAlias } },
      }),
    );
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

  it("plugin POST rejects an invalid phase with a typed 400", async () => {
    try {
      unwrap(
        await admin.POST("/api/v1/plugins", {
          body: {
            name: unique("plg"),
            phase: "invalid" as "pre",
            enabled: true,
          },
        }),
      );
      throw new Error("expected unwrap to throw on a 400");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(400);
    }
  });

  it("plugin create / get / delete round-trip", async () => {
    const name = unique("plg");
    unwrap(
      await admin.POST("/api/v1/plugins", {
        body: { name, phase: "pre", enabled: true, params: { rpm: 100 } },
      }),
    );
    const got = unwrap(
      await admin.GET("/api/v1/plugins/{name}", {
        params: { path: { name }, query: { scope: "" } },
      }),
    );
    expect(got.name).toBe(name);
    expect(got.phase).toBe("pre");
    unwrap(
      await admin.DELETE("/api/v1/plugins/{name}", {
        params: { path: { name }, query: { scope: "" } },
      }),
    );
    // After delete, get returns 404.
    try {
      unwrap(
        await admin.GET("/api/v1/plugins/{name}", {
          params: { path: { name }, query: { scope: "" } },
        }),
      );
      throw new Error("expected 404");
    } catch (err) {
      expect(err).toBeInstanceOf(AdminError);
      expect((err as AdminError).status).toBe(404);
    }
  });

  it("routes list paginates with a stable next_cursor", async () => {
    // Seed enough routes to exceed one page (limit=2 → 2 rows + cursor).
    const pName = unique("pp");
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
    const aliases: string[] = [];
    for (const a of ["r1", "r2", "r3"]) {
      const alias = unique(a);
      aliases.push(alias);
      unwrap(
        await admin.POST("/api/v1/models", {
          body: {
            alias,
            upstreams: [{ provider: pName, upstream_model: "gpt-4o" }],
          },
        }),
      );
      unwrap(
        await admin.POST("/api/v1/routes", {
          body: {
            model_alias: alias,
            strategy: "priority",
            providers: [{ name: pName }],
          },
        }),
      );
    }
    const page1 = unwrap(
      await admin.GET("/api/v1/routes", {
        params: { query: { limit: 2 } },
      }),
    );
    expect(page1.data).toHaveLength(2);
    expect(typeof page1.next_cursor).toBe("string");
    expect(page1.next_cursor).not.toBe("");
    const page2 = unwrap(
      await admin.GET("/api/v1/routes", {
        params: { query: { limit: 2, cursor: page1.next_cursor } },
      }),
    );
    expect(page2.data.length).toBeGreaterThan(0);
    // Cleanup in reverse order.
    for (const alias of aliases.reverse()) {
      unwrap(
        await admin.DELETE("/api/v1/routes/{alias}", {
          params: { path: { alias } },
        }),
      );
      unwrap(
        await admin.DELETE("/api/v1/models/{alias}", {
          params: { path: { alias } },
        }),
      );
    }
    unwrap(
      await admin.DELETE("/api/v1/providers/{name}", {
        params: { path: { name: pName } },
      }),
    );
  });
});
