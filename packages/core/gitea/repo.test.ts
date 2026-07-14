import { describe, it, expect } from "vitest";
import { giteaHostMatches } from "./repo";

describe("giteaHostMatches", () => {
  const base = "https://gitea.internal.example.com";

  it("matches an https repo URL on the same host", () => {
    expect(giteaHostMatches("https://gitea.internal.example.com/acme/widget.git", base)).toBe(true);
  });

  it("matches scp-style shorthand", () => {
    expect(giteaHostMatches("git@gitea.internal.example.com:acme/widget.git", base)).toBe(true);
  });

  it("matches an ssh URL with a different port than the base URL", () => {
    expect(giteaHostMatches("ssh://git@gitea.internal.example.com:2222/acme/widget.git", base)).toBe(true);
  });

  it("is case-insensitive", () => {
    expect(giteaHostMatches("https://GITEA.internal.example.com/acme/widget.git", base)).toBe(true);
  });

  it("does not match a different host", () => {
    expect(giteaHostMatches("https://github.com/acme/widget.git", base)).toBe(false);
  });

  it("returns false for an empty repo URL or base URL", () => {
    expect(giteaHostMatches("", base)).toBe(false);
    expect(giteaHostMatches("https://gitea.internal.example.com/acme/widget.git", "")).toBe(false);
  });

  it("returns false for a malformed base URL", () => {
    expect(giteaHostMatches("https://gitea.internal.example.com/acme/widget.git", "not-a-url")).toBe(false);
  });
});
