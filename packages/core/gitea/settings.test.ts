import { describe, it, expect } from "vitest";
import { deriveGiteaSettings } from "./settings";
import type { Workspace } from "../types";

function ws(settings: Record<string, unknown>): Pick<Workspace, "settings"> {
  return { settings };
}

describe("deriveGiteaSettings", () => {
  it("defaults every flag to true when workspace is null", () => {
    expect(deriveGiteaSettings(null)).toEqual({
      enabled: true,
      prSidebar: true,
      coAuthor: true,
      autoLinkPRs: true,
    });
  });

  it("defaults every flag to true on empty settings", () => {
    expect(deriveGiteaSettings(ws({}))).toEqual({
      enabled: true,
      prSidebar: true,
      coAuthor: true,
      autoLinkPRs: true,
    });
  });

  it("master switch off forces every dependent flag off", () => {
    const got = deriveGiteaSettings(
      ws({
        gitea_enabled: false,
        gitea_pr_sidebar_enabled: true,
        co_authored_by_enabled: true,
        gitea_auto_link_prs_enabled: true,
      }),
    );
    expect(got).toEqual({
      enabled: false,
      prSidebar: false,
      coAuthor: false,
      autoLinkPRs: false,
    });
  });

  it("each sub-flag can be flipped independently when master is on", () => {
    expect(
      deriveGiteaSettings(ws({ gitea_pr_sidebar_enabled: false })),
    ).toMatchObject({ enabled: true, prSidebar: false, coAuthor: true, autoLinkPRs: true });

    expect(
      deriveGiteaSettings(ws({ co_authored_by_enabled: false })),
    ).toMatchObject({ enabled: true, prSidebar: true, coAuthor: false, autoLinkPRs: true });

    expect(
      deriveGiteaSettings(ws({ gitea_auto_link_prs_enabled: false })),
    ).toMatchObject({ enabled: true, prSidebar: true, coAuthor: true, autoLinkPRs: false });
  });

  it("treats non-false values (true, null, missing) as enabled", () => {
    expect(
      deriveGiteaSettings(ws({ gitea_enabled: true, gitea_pr_sidebar_enabled: null })),
    ).toMatchObject({ enabled: true, prSidebar: true });
  });

  it("co_authored_by_enabled is the SAME key GitHub's derivation reads — a change from either tab affects both", () => {
    // Regression guard: this must stay `co_authored_by_enabled`, not a
    // gitea-prefixed variant, because the underlying prepare-commit-msg hook
    // is provider-agnostic (see repocache/cache.go) and both settings tabs
    // intentionally share one switch.
    expect(
      deriveGiteaSettings(ws({ co_authored_by_enabled: false })).coAuthor,
    ).toBe(false);
  });
});
