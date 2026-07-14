import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { RepoProviderIcon, repoProviderResourceType } from "./repo-provider-icon";

const GITEA_BASE_URL = "https://gitea.internal.example.com";

describe("repoProviderResourceType", () => {
  it("returns gitea_repo when the URL matches the connected Gitea host", () => {
    expect(
      repoProviderResourceType("https://gitea.internal.example.com/acme/widget.git", GITEA_BASE_URL),
    ).toBe("gitea_repo");
  });

  it("returns github_repo when the URL doesn't match the connected Gitea host", () => {
    expect(repoProviderResourceType("https://github.com/acme/widget.git", GITEA_BASE_URL)).toBe(
      "github_repo",
    );
  });

  it("returns github_repo when no Gitea instance is connected", () => {
    expect(
      repoProviderResourceType("https://gitea.internal.example.com/acme/widget.git", undefined),
    ).toBe("github_repo");
  });
});

describe("RepoProviderIcon", () => {
  it("renders the Gitea mark for a matching host", () => {
    const { container } = render(
      <RepoProviderIcon url="https://gitea.internal.example.com/acme/widget.git" giteaBaseUrl={GITEA_BASE_URL} />,
    );
    expect(container.querySelector("img")).toBeTruthy();
  });

  it("renders the GitHub mark for a non-matching host", () => {
    const { container } = render(
      <RepoProviderIcon url="https://github.com/acme/widget.git" giteaBaseUrl={GITEA_BASE_URL} />,
    );
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.querySelector("img")).toBeFalsy();
  });

  it("renders the GitHub mark when no Gitea instance is connected", () => {
    const { container } = render(
      <RepoProviderIcon url="https://gitea.internal.example.com/acme/widget.git" giteaBaseUrl={undefined} />,
    );
    expect(container.querySelector("svg")).toBeTruthy();
  });
});
