import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ProjectResource } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enProjects from "../../locales/en/projects.json";

const TEST_RESOURCES = { en: { common: enCommon, projects: enProjects } };

vi.mock("../../platform", () => ({
  isDesktopShell: () => false,
  useLocalDaemonStatus: () => ({ daemonId: null, deviceName: null, running: false }),
  pickDirectory: vi.fn(),
  validateLocalDirectory: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const mockWorkspaceRepos = vi.hoisted(() => [
  { url: "https://gitea.internal.example.com/acme/widget.git" },
  { url: "https://github.com/acme/other.git" },
]);

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", repos: mockWorkspaceRepos }),
}));

const mockListResources = vi.hoisted(() => vi.fn());
const mockCreateResource = vi.hoisted(() => vi.fn());
const mockGetGiteaConnection = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    listProjectResources: (...args: unknown[]) => mockListResources(...args),
    createProjectResource: (...args: unknown[]) => mockCreateResource(...args),
    getGiteaConnection: (...args: unknown[]) => mockGetGiteaConnection(...args),
  },
}));

import { ProjectResourcesSection } from "./project-resources-section";

function renderSection() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ProjectResourcesSection projectId="proj-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function makeGiteaResource(url: string): ProjectResource {
  return {
    id: `res-${url}`,
    project_id: "proj-1",
    workspace_id: "ws-1",
    resource_type: "gitea_repo",
    resource_ref: { url },
    label: null,
    position: 0,
    created_at: new Date(0).toISOString(),
    created_by: null,
  };
}

describe("ProjectResourcesSection — Gitea support", () => {
  beforeEach(() => {
    mockListResources.mockReset().mockResolvedValue({ resources: [], total: 0 });
    mockCreateResource.mockReset().mockResolvedValue(makeGiteaResource("x"));
    mockGetGiteaConnection.mockReset().mockResolvedValue({
      connection: { id: "conn-1", account_login: "acme" },
      configured: true,
      can_manage: true,
      base_url: "https://gitea.internal.example.com",
    });
  });

  it("attaches a repo on the connected Gitea host as gitea_repo", async () => {
    const user = userEvent.setup();
    renderSection();

    await user.click(screen.getByRole("button", { name: /Add resource/i }));
    await waitFor(() => {
      expect(screen.getByText(/gitea\.internal\.example\.com\/acme\/widget/)).toBeInTheDocument();
    });
    await user.click(screen.getByText(/gitea\.internal\.example\.com\/acme\/widget/));

    await waitFor(() => {
      expect(mockCreateResource).toHaveBeenCalledWith("proj-1", {
        resource_type: "gitea_repo",
        resource_ref: { url: "https://gitea.internal.example.com/acme/widget.git" },
      });
    });
  });

  it("attaches a repo not on the Gitea host as github_repo", async () => {
    const user = userEvent.setup();
    renderSection();

    await user.click(screen.getByRole("button", { name: /Add resource/i }));
    await waitFor(() => {
      expect(screen.getByText(/github\.com\/acme\/other/)).toBeInTheDocument();
    });
    await user.click(screen.getByText(/github\.com\/acme\/other/));

    await waitFor(() => {
      expect(mockCreateResource).toHaveBeenCalledWith("proj-1", {
        resource_type: "github_repo",
        resource_ref: { url: "https://github.com/acme/other.git" },
      });
    });
  });

  it("treats an already-attached gitea_repo as attached in the picker (regression: used to only check github_repo)", async () => {
    const user = userEvent.setup();
    mockListResources.mockResolvedValue({
      resources: [makeGiteaResource("https://gitea.internal.example.com/acme/widget.git")],
      total: 1,
    });
    renderSection();

    await user.click(screen.getByRole("button", { name: /Add resource/i }));
    // Two occurrences: the already-attached row in the main list, and the
    // picker row in the popover — both render before the fix too, so the
    // real assertion is the "attached" badge on the picker row below.
    await waitFor(() => {
      expect(screen.getAllByText(/gitea\.internal\.example\.com\/acme\/widget/).length).toBe(2);
    });
    expect(screen.getByText("attached")).toBeInTheDocument();
  });
});
