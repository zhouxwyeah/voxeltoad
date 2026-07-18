import { describe, expect, it } from "vitest";
import { VoxeltoadGateway } from "../src";

describe("VoxeltoadGateway construction", () => {
  it("builds a client with the provided baseURL and apiKey", () => {
    const client = new VoxeltoadGateway({
      apiKey: "test-key",
      baseURL: "http://localhost:8080/v1",
    });
    expect(client.baseURL).toBe("http://localhost:8080/v1");
  });

  it("exposes the OpenAI-compatible chat surface", () => {
    const client = new VoxeltoadGateway({
      apiKey: "test-key",
      baseURL: "http://localhost:8080/v1",
    });
    expect(typeof client.chat.completions.create).toBe("function");
  });
});
