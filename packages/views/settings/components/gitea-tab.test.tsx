import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockUpdateWorkspace = vi.hoisted(() => vi.fn());
const mockRegisterConnection = vi.hoisted(() => vi.fn());
const mockDeleteConnection = vi.hoisted(() => vi.fn());
const mockSyncRepositories = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());
const mockNavPush = vi.hoisted(() => vi.fn());
const mockSetQueryData = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());

const workspaceRef = vi.hoisted(() => ({
  current: {
    id: "workspace-1",
    name: "Acme",
    slug: "acme",
    settings: {} as Record<string, unknown>,
    repos: [{ url: "https://gitea.internal.example.com/acme/api" }] as { url: string }[],
  },
}));
type MemberRole = "owner" | "admin" | "member" | "guest";
const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const connectionRef = vi.hoisted(() => ({
  current: {
    connection: null as { id: string; account_login: string } | null,
    configured: true,
    can_manage: true as boolean,
    base_url: "https://gitea.internal.example.com",
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current };
    if (key.includes("connection")) return { data: connectionRef.current };
    return { data: undefined };
  },
  useQueryClient: () => ({
    setQueryData: mockSetQueryData,
    invalidateQueries: mockInvalidate,
  }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => workspaceRef.current,
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
  workspaceKeys: { list: () => ["workspaces"] },
}));

vi.mock("@multica/core/gitea", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/gitea")>("@multica/core/gitea");
  return {
    ...actual,
    giteaConnectionOptions: () => ({
      queryKey: ["gitea", "workspace-1", "connection"],
      queryFn: vi.fn(),
    }),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    updateWorkspace: mockUpdateWorkspace,
    registerGiteaConnection: mockRegisterConnection,
    deleteGiteaConnection: mockDeleteConnection,
    syncGiteaRepositories: mockSyncRepositories,
  },
}));

vi.mock("@multica/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: mockNavPush,
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/settings",
    searchParams: new URLSearchParams("tab=gitea"),
    getShareableUrl: (p: string) => `https://app.example${p}`,
  }),
}));

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: vi.fn() },
}));

import { GiteaTab } from "./gitea-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function resetFixtures() {
  vi.clearAllMocks();
  workspaceRef.current = {
    id: "workspace-1",
    name: "Acme",
    slug: "acme",
    settings: {},
    repos: [{ url: "https://gitea.internal.example.com/acme/api" }],
  };
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  connectionRef.current = {
    connection: null,
    configured: true,
    can_manage: true,
    base_url: "https://gitea.internal.example.com",
  };
}

describe("GiteaTab", () => {
  beforeEach(resetFixtures);

  it("disables every feature switch when the master switch is off", () => {
    workspaceRef.current.settings = { gitea_enabled: false };
    render(<GiteaTab />, { wrapper: I18nWrapper });

    const master = screen.getByRole("switch", { name: /enable gitea features/i });
    expect(master.getAttribute("aria-checked")).toBe("false");

    const featureSwitches = [
      screen.getByRole("switch", { name: /pull request sidebar/i }),
      screen.getByRole("switch", { name: /co-authored-by trailer/i }),
      screen.getByRole("switch", { name: /auto-link issues and prs/i }),
    ];
    for (const sw of featureSwitches) {
      const ariaDisabled = sw.getAttribute("aria-disabled");
      const disabled = sw.hasAttribute("disabled");
      expect(ariaDisabled === "true" || disabled).toBe(true);
    }
  });

  it("flipping Co-authored-by persists the SAME co_authored_by_enabled key GitHub uses (not a gitea-prefixed variant)", async () => {
    const user = userEvent.setup();
    workspaceRef.current.settings = { gitea_pr_sidebar_enabled: true };
    mockUpdateWorkspace.mockResolvedValue({
      ...workspaceRef.current,
      settings: { gitea_pr_sidebar_enabled: true, co_authored_by_enabled: false },
    });

    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("switch", { name: /co-authored-by trailer/i }));

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        settings: { gitea_pr_sidebar_enabled: true, co_authored_by_enabled: false },
      });
    });
  });

  it("flipping PR sidebar persists gitea_pr_sidebar_enabled", async () => {
    const user = userEvent.setup();
    mockUpdateWorkspace.mockResolvedValue({
      ...workspaceRef.current,
      settings: { gitea_pr_sidebar_enabled: false },
    });

    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("switch", { name: /pull request sidebar/i }));

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        settings: { gitea_pr_sidebar_enabled: false },
      });
    });
  });

  it("flipping the master switch off persists gitea_enabled=false and merges existing settings", async () => {
    const user = userEvent.setup();
    workspaceRef.current.settings = { gitea_auto_link_prs_enabled: true };
    mockUpdateWorkspace.mockResolvedValue({
      ...workspaceRef.current,
      settings: { gitea_auto_link_prs_enabled: true, gitea_enabled: false },
    });

    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("switch", { name: /enable gitea features/i }));

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        settings: { gitea_auto_link_prs_enabled: true, gitea_enabled: false },
      });
    });
  });

  it("connecting opens a dialog and posts the pasted token", async () => {
    const user = userEvent.setup();
    mockRegisterConnection.mockResolvedValue({
      connection: { id: "conn-1", account_login: "octocat" },
    });

    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("button", { name: /^Connect Gitea$/ }));
    expect(screen.getByTestId("gitea-connect-dialog")).toBeTruthy();

    await user.type(screen.getByTestId("gitea-connect-token"), "my-pat-token");
    await user.click(screen.getByRole("button", { name: /^Connect$/ }));

    await waitFor(() => {
      expect(mockRegisterConnection).toHaveBeenCalledWith("workspace-1", "my-pat-token");
    });
  });

  it("Connect button is disabled when the deployment is not configured", () => {
    connectionRef.current = {
      connection: null,
      configured: false,
      can_manage: true,
      base_url: "",
    };
    render(<GiteaTab />, { wrapper: I18nWrapper });
    expect(screen.getByRole("button", { name: /^Connect Gitea$/ })).toBeDisabled();
  });

  it("clicking Disconnect opens the confirmation and only fires on confirm", async () => {
    const user = userEvent.setup();
    connectionRef.current = {
      connection: { id: "conn-1", account_login: "acme-org" },
      configured: true,
      can_manage: true,
      base_url: "https://gitea.internal.example.com",
    };
    mockDeleteConnection.mockResolvedValue(undefined);

    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("button", { name: /^Disconnect$/ }));
    expect(screen.getByText(/Multica will remove the webhooks/i)).toBeTruthy();
    expect(mockDeleteConnection).not.toHaveBeenCalled();

    const dialogConfirm = screen
      .getAllByRole("button", { name: /^Disconnect$/ })
      .find((b) => b.getAttribute("data-slot")?.includes("alert-dialog"));
    await user.click(dialogConfirm ?? screen.getAllByRole("button", { name: /^Disconnect$/ })[1]!);

    await waitFor(() => {
      expect(mockDeleteConnection).toHaveBeenCalledWith("workspace-1");
    });
  });

  it("syncing repositories calls the sync endpoint", async () => {
    const user = userEvent.setup();
    connectionRef.current = {
      connection: { id: "conn-1", account_login: "acme-org" },
      configured: true,
      can_manage: true,
      base_url: "https://gitea.internal.example.com",
    };
    mockSyncRepositories.mockResolvedValue({ created: 1, removed: 0 });

    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("button", { name: /Sync repositories/ }));

    await waitFor(() => {
      expect(mockSyncRepositories).toHaveBeenCalledWith("workspace-1");
    });
  });

  it("non-admin sees the existing connection but no manage controls", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    connectionRef.current = {
      connection: { id: "conn-1", account_login: "acme-org" },
      configured: true,
      can_manage: false,
      base_url: "https://gitea.internal.example.com",
    };
    render(<GiteaTab />, { wrapper: I18nWrapper });

    expect(screen.getByText(/Connected to acme-org/i)).toBeTruthy();
    expect(screen.getByText(/Read-only view\./i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Connect Gitea$/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Disconnect$/ })).toBeNull();
  });

  it("repositories shortcut navigates to the repositories tab", async () => {
    const user = userEvent.setup();
    render(<GiteaTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("button", { name: /Manage repositories/ }));
    expect(mockNavPush).toHaveBeenCalledWith("/acme/settings?tab=repositories");
  });
});
