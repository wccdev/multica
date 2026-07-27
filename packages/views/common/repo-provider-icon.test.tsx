import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { RepoProviderIcon, repoProviderResourceType } from "./repo-provider-icon";

describe("repoProviderResourceType", () => {
  it("always returns github_repo", () => {
    expect(repoProviderResourceType()).toBe("github_repo");
  });
});

describe("RepoProviderIcon", () => {
  it("renders the GitHub mark", () => {
    const { container } = render(<RepoProviderIcon />);
    expect(container.querySelector("svg")).toBeTruthy();
  });
});
