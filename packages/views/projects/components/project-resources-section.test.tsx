import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
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
  { url: "https://github.com/acme/widget.git" },
  { url: "https://github.com/acme/other.git" },
]);

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", repos: mockWorkspaceRepos }),
}));

const mockListResources = vi.hoisted(() => vi.fn());
const mockCreateResource = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    listProjectResources: (...args: unknown[]) => mockListResources(...args),
    createProjectResource: (...args: unknown[]) => mockCreateResource(...args),
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

describe("ProjectResourcesSection", () => {
  beforeEach(() => {
    mockListResources.mockReset().mockResolvedValue({ resources: [], total: 0 });
    mockCreateResource.mockReset().mockResolvedValue({
      id: "res-1",
      project_id: "proj-1",
      workspace_id: "ws-1",
      resource_type: "github_repo",
      resource_ref: { url: "https://github.com/acme/other.git" },
      label: null,
      position: 0,
      created_at: new Date(0).toISOString(),
      created_by: null,
    });
  });

  it("attaches a picked workspace repo as github_repo", async () => {
    const user = userEvent.setup();
    renderSection();

    await user.click(screen.getByRole("button", { name: /Add resource/i }));
    // githubShortLabel shortens github.com URLs to "owner/repo" (MUL-5221).
    await waitFor(() => {
      expect(screen.getByText(/^acme\/other$/)).toBeInTheDocument();
    });
    await user.click(screen.getByText(/^acme\/other$/));

    await waitFor(() => {
      expect(mockCreateResource).toHaveBeenCalledWith("proj-1", {
        resource_type: "github_repo",
        resource_ref: { url: "https://github.com/acme/other.git" },
      });
    });
  });

  it("treats an already-attached repo as attached in the picker", async () => {
    const user = userEvent.setup();
    mockListResources.mockResolvedValue({
      resources: [
        {
          id: "res-existing",
          project_id: "proj-1",
          workspace_id: "ws-1",
          resource_type: "github_repo",
          resource_ref: { url: "https://github.com/acme/widget.git" },
          label: null,
          position: 0,
          created_at: new Date(0).toISOString(),
          created_by: null,
        },
      ],
      total: 1,
    });
    renderSection();

    await user.click(screen.getByRole("button", { name: /Add resource/i }));
    // Two occurrences: the already-attached row in the main list, and the
    // picker row in the popover — the real assertion is the "attached" badge
    // on the picker row below.
    await waitFor(() => {
      expect(screen.getAllByText(/^acme\/widget$/).length).toBe(2);
    });
    expect(screen.getByText("attached")).toBeInTheDocument();
  });
});
