import { beforeEach, describe, expect, it } from "vitest";
import { VoxeltoadGateway } from "../../src";

/**
 * E2E contract tests. These exercise the SDK against a running gateway with
 * mock upstreams (see design/e2e.md). They are skipped unless VOXELTOAD_E2E
 * is set, so the default `yarn test` stays hermetic.
 *
 * To run: start the gateway + mock upstreams, then
 *   VOXELTOAD_E2E=1 yarn test:e2e
 *
 * The mock upstream exposes a control endpoint (POST /__set, POST /__reset)
 * that vitest drives per-test to inject usage / errors / chunking.
 */
const e2eEnabled = process.env.VOXELTOAD_E2E === "1";
const baseURL = process.env.VOXELTOAD_BASE_URL ?? "http://localhost:8080/v1";
const apiKey = process.env.VOXELTOAD_API_KEY ?? "test-admin-key";
const model = process.env.VOXELTOAD_MODEL ?? "chat";
const mockControlURL = process.env.VOXELTOAD_MOCK_CONTROL_URL;

const messages = [{ role: "user" as const, content: "hi" }];

describe.skipIf(!e2eEnabled)("chat completions (mock upstream)", () => {
  const client = new VoxeltoadGateway({ apiKey, baseURL });

  beforeEach(async () => {
    if (mockControlURL) {
      await fetch(`${mockControlURL}/__reset`, { method: "POST" });
    }
  });

  // ── non-streaming ──────────────────────────────────────────────

  it("non-streaming completion returns content and bills usage", async () => {
    const res = await client.chat.completions.create({ model, messages });
    expect(res.choices[0]?.message?.content).toBe(
      "hello from the devstack mock upstream",
    );
    expect(res.usage?.total_tokens).toBe(18);
  });

  // ── streaming ──────────────────────────────────────────────────

  it("streaming completion stitches chunks and aggregates usage", async () => {
    const stream = await client.chat.completions.create({
      model,
      messages,
      stream: true,
    });
    let text = "";
    let usage: { total_tokens?: number } = {};
    for await (const c of stream) {
      text += c.choices[0]?.delta?.content ?? "";
      // The final chunk carries usage (OpenAI convention).
      if (c.usage) usage = c.usage;
    }
    expect(text).toBe("hello from stream");
    expect(usage.total_tokens).toBe(18);
  });

  // ── auth ───────────────────────────────────────────────────────

  it("unauthorized key is rejected with 401", async () => {
    const badClient = new VoxeltoadGateway({
      apiKey: "sk-does-not-exist",
      baseURL,
    });
    await expect(
      badClient.chat.completions.create({ model, messages }),
    ).rejects.toMatchObject({ status: 401 });
  });

  // ── injected (mock control endpoint) ───────────────────────────

  async function setMockState(body: Record<string, unknown>) {
    if (!mockControlURL) throw new Error("VOXELTOAD_MOCK_CONTROL_URL not set");
    const res = await fetch(`${mockControlURL}/__set`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    expect(res.status).toBe(204);
  }

  it("injected usage is reflected in response", async () => {
    await setMockState({
      usage: { prompt_tokens: 5, completion_tokens: 2, total_tokens: 7 },
    });
    const res = await client.chat.completions.create({ model, messages });
    expect(res.usage?.total_tokens).toBe(7);
    expect(res.usage?.prompt_tokens).toBe(5);
    expect(res.usage?.completion_tokens).toBe(2);
  });

  it.each([429, 500])(
    "upstream %i surfaces as 502 (gateway maps 4xx/5xx → Bad Gateway)",
    async (status) => {
      await setMockState({ errorStatus: status });
      await expect(
        client.chat.completions.create({ model, messages }),
      ).rejects.toMatchObject({ status: 502 });
    },
  );

  it("error before first stream byte surfaces as 502", async () => {
    await setMockState({ firstByteError: true });
    await expect(
      client.chat.completions.create({ model, messages, stream: true }),
    ).rejects.toMatchObject({ status: 502 });
  });

  it("mid-stream break truncates content without throwing (gateway sends [DONE])", async () => {
    await setMockState({
      chunks: ["Hel", "lo"],
      midStreamBreak: true,
      chunkDelay: 0,
    });
    const stream = await client.chat.completions.create({
      model,
      messages,
      stream: true,
    });
    let text = "";
    for await (const c of stream) {
      text += c.choices[0]?.delta?.content ?? "";
    }
    // Only the first chunk was relayed before the break.
    expect(text).toBe("Hel");
    // Reaching here without a throw proves the stream ended normally
    // (gateway defers [DONE] on mid-stream error — stream.go:79-84).
  });

  // TODO(ADR-0011): Claude SSE cross-provider consistency — the mock
  // upstream only speaks OpenAI shapes. Claude SSE → OpenAI conversion
  // is a gateway adapter concern, covered by Go `make test-e2e`.
});
