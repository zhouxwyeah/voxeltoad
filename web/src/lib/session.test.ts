import { describe, it, expect, vi, beforeEach } from "vitest";

// SESSION_SECRET is required by sessionOptions() at request time.
process.env.SESSION_SECRET = "a".repeat(32);

vi.mock("server-only", () => ({}));

vi.mock("next/headers", () => ({
  cookies: vi.fn(() => ({})),
}));

let mockSession: Record<string, unknown> & { save: ReturnType<typeof vi.fn> };

vi.mock("iron-session", () => ({
  getIronSession: vi.fn(async () => mockSession),
}));

import { setSession, getSession } from "./session";

describe("session", () => {
  beforeEach(() => {
    mockSession = {
      save: vi.fn(),
    };
  });

  it("setSession persists tenantName from /me response", async () => {
    await setSession({
      token: "tok",
      email: "a@b.com",
      role: "tenant-admin",
      tenantName: "acme",
    });

    expect(mockSession.tenantName).toBe("acme");
    expect(mockSession.save).toHaveBeenCalled();
  });

  it("getSession returns the previously stored tenantName", async () => {
    mockSession = {
      token: "tok",
      email: "a@b.com",
      role: "tenant-admin",
      tenantName: "acme",
      save: vi.fn(),
    };

    const session = await getSession();
    expect(session.tenantName).toBe("acme");
  });
});
