"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ExternalLink, GitCommitHorizontal, Link2, PanelRight, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace } from "@multica/core/paths";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { deriveGiteaSettings, giteaConnectionOptions, giteaKeys } from "@multica/core/gitea";
import { api } from "@multica/core/api";
import type { Workspace } from "@multica/core/types";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { SettingsTab } from "./settings-layout";
import { GiteaMark } from "./gitea-mark";

type SettingsKey =
  | "gitea_enabled"
  | "gitea_pr_sidebar_enabled"
  | "co_authored_by_enabled"
  | "gitea_auto_link_prs_enabled";

export function GiteaTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const navigation = useNavigation();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canView = !!currentMember;

  const { data: connectionData } = useQuery({
    ...giteaConnectionOptions(wsId),
    enabled: !!wsId && canView,
  });
  const connection = connectionData?.connection ?? null;
  const configured = connectionData?.configured ?? false;
  const canManage = connectionData?.can_manage === true;
  const baseUrl = connectionData?.base_url ?? "";
  const connected = !!connection;

  const flags = deriveGiteaSettings(workspace);
  const [savingKey, setSavingKey] = useState<SettingsKey | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [token, setToken] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [disconnectOpen, setDisconnectOpen] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);

  async function persistSetting(key: SettingsKey, next: boolean) {
    if (!workspace || savingKey) return;
    setSavingKey(key);
    try {
      const merged = {
        ...((workspace.settings as Record<string, unknown>) ?? {}),
        [key]: next,
      };
      const updated = await api.updateWorkspace(workspace.id, { settings: merged });
      qc.setQueryData(workspaceKeys.list(), (old: Workspace[] | undefined) =>
        old?.map((ws) => (ws.id === updated.id ? updated : ws)),
      );
      toast.success(t(($) => $.auto_save.toast_saved), { id: "settings-auto-save" });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.gitea.toast_failed));
    } finally {
      setSavingKey(null);
    }
  }

  function closeDialog() {
    if (connecting) return;
    setDialogOpen(false);
    setToken("");
  }

  async function handleConnect() {
    const trimmed = token.trim();
    if (connecting || !trimmed) return;
    setConnecting(true);
    try {
      await api.registerGiteaConnection(wsId, trimmed);
      await qc.invalidateQueries({ queryKey: giteaKeys.connection(wsId) });
      toast.success(t(($) => $.gitea.connect_success_toast));
      setDialogOpen(false);
      setToken("");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.gitea.connect_failed_toast));
    } finally {
      setConnecting(false);
    }
  }

  async function handleSync() {
    if (syncing) return;
    setSyncing(true);
    try {
      const result = await api.syncGiteaRepositories(wsId);
      toast.success(
        t(($) => $.gitea.sync_success_toast, { created: result.created, removed: result.removed }),
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.gitea.sync_failed_toast));
    } finally {
      setSyncing(false);
    }
  }

  async function handleDisconnect() {
    if (disconnecting) return;
    setDisconnecting(true);
    try {
      await api.deleteGiteaConnection(wsId);
      await qc.invalidateQueries({ queryKey: giteaKeys.connection(wsId) });
      toast.success(t(($) => $.gitea.toast_disconnected));
      setDisconnectOpen(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.gitea.toast_disconnect_failed));
    } finally {
      setDisconnecting(false);
    }
  }

  if (!workspace) return null;

  const repositoriesHref = `${navigation.pathname}?tab=repositories`;

  return (
    <SettingsTab title={t(($) => $.page.tabs.gitea)} description={t(($) => $.gitea.page_description)}>
      <section className="space-y-3">
        <Card>
          <CardContent>
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground">
                  <GiteaMark className="h-4 w-4" />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="gitea-master" className="text-sm font-medium">
                    {t(($) => $.gitea.section_master)}
                  </Label>
                  <p className="text-sm text-muted-foreground">
                    {flags.enabled
                      ? t(($) => $.gitea.master_description_on)
                      : t(($) => $.gitea.master_description_off)}
                  </p>
                </div>
              </div>
              <Switch
                id="gitea-master"
                checked={flags.enabled}
                onCheckedChange={(v) => persistSetting("gitea_enabled", v)}
                disabled={!canManage || savingKey === "gitea_enabled"}
              />
            </div>
          </CardContent>
        </Card>
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t(($) => $.gitea.section_connection)}</h2>
        <Card>
          <CardContent className="space-y-4">
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <GiteaMark className="h-6 w-6 mt-0.5 shrink-0" />
                <div className="space-y-1">
                  <p className="text-sm font-medium">{t(($) => $.gitea.connection_title)}</p>
                  {connected ? (
                    <>
                      <p className="text-xs text-muted-foreground">
                        {t(($) => $.gitea.connected_to, { login: connection.account_login })}
                      </p>
                      {baseUrl && (
                        <p className="text-xs text-muted-foreground">{baseUrl}</p>
                      )}
                    </>
                  ) : canManage ? (
                    <p className="text-xs text-muted-foreground">
                      {t(($) => $.gitea.connection_description)}
                      {baseUrl ? ` (${baseUrl})` : ""}
                    </p>
                  ) : (
                    <p className="text-xs text-muted-foreground">
                      {t(($) => $.gitea.contact_admin_to_connect)}
                    </p>
                  )}
                </div>
              </div>
              {canManage && (
                <div className="flex items-center gap-2">
                  {connected ? (
                    <>
                      <Button variant="outline" size="sm" onClick={handleSync} disabled={syncing}>
                        <RefreshCw className="h-3 w-3" />
                        {syncing ? t(($) => $.gitea.syncing) : t(($) => $.gitea.sync_repositories)}
                      </Button>
                      <Button variant="outline" size="sm" onClick={() => setDisconnectOpen(true)}>
                        {t(($) => $.gitea.disconnect)}
                      </Button>
                    </>
                  ) : (
                    <Button
                      size="sm"
                      onClick={() => setDialogOpen(true)}
                      disabled={!configured}
                      title={!configured ? t(($) => $.gitea.connect_disabled_tooltip) : undefined}
                    >
                      {t(($) => $.gitea.connect_gitea)}
                    </Button>
                  )}
                </div>
              )}
            </div>

            {canManage && !configured && (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.gitea.not_configured)}{" "}
                <code className="rounded bg-muted px-1 py-0.5 text-[10px]">GITEA_BASE_URL</code>,{" "}
                <code className="rounded bg-muted px-1 py-0.5 text-[10px]">GITEA_WEBHOOK_SECRET</code>{" "}
                {t(($) => $.gitea.not_configured_and)}{" "}
                <code className="rounded bg-muted px-1 py-0.5 text-[10px]">MULTICA_GITEA_SECRET_KEY</code>.
              </p>
            )}

            {!canManage && connected && (
              <p className="text-xs text-muted-foreground">{t(($) => $.gitea.read_only_hint)}</p>
            )}
          </CardContent>
        </Card>
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t(($) => $.gitea.section_features)}</h2>
        <Card className="gap-0 py-0">
          <CardContent className="divide-y divide-surface-border px-0">
            <FeatureRow
              id="gitea-pr-sidebar"
              icon={<PanelRight className="h-4 w-4" />}
              label={t(($) => $.gitea.feature_pr_sidebar_label)}
              description={
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.gitea.feature_pr_sidebar_description)}
                </p>
              }
              checked={flags.prSidebar}
              disabled={!canManage || !flags.enabled || savingKey === "gitea_pr_sidebar_enabled"}
              onCheckedChange={(v) => persistSetting("gitea_pr_sidebar_enabled", v)}
            />

            <FeatureRow
              id="gitea-coauthor"
              icon={<GitCommitHorizontal className="h-4 w-4" />}
              label={t(($) => $.gitea.feature_co_author_label)}
              description={
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.gitea.feature_co_author_description_prefix)}{" "}
                  <code className="rounded bg-muted px-1 py-0.5 text-xs">
                    {"Co-authored-by: multica-agent <agent@multica.ai>"}
                  </code>{" "}
                  {t(($) => $.gitea.feature_co_author_description_suffix)}
                </p>
              }
              checked={flags.coAuthor}
              disabled={!canManage || !flags.enabled || savingKey === "co_authored_by_enabled"}
              onCheckedChange={(v) => persistSetting("co_authored_by_enabled", v)}
            />

            <FeatureRow
              id="gitea-auto-link"
              icon={<Link2 className="h-4 w-4" />}
              label={t(($) => $.gitea.feature_auto_link_label)}
              description={
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.gitea.feature_auto_link_description)}
                </p>
              }
              checked={flags.autoLinkPRs}
              disabled={!canManage || !flags.enabled || savingKey === "gitea_auto_link_prs_enabled"}
              onCheckedChange={(v) => persistSetting("gitea_auto_link_prs_enabled", v)}
            />
          </CardContent>
        </Card>
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t(($) => $.gitea.section_repositories)}</h2>
        <Card>
          <CardContent>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="text-sm font-medium">{t(($) => $.gitea.repositories_shortcut_label)}</p>
              <Button variant="outline" size="sm" onClick={() => navigation.push(repositoriesHref)}>
                <ExternalLink className="h-3 w-3" />
                {t(($) => $.gitea.repositories_shortcut_link)}
              </Button>
            </div>
          </CardContent>
        </Card>
      </section>

      <Dialog open={dialogOpen} onOpenChange={(v) => (v ? setDialogOpen(true) : closeDialog())}>
        <DialogContent className="sm:max-w-lg" data-testid="gitea-connect-dialog">
          <DialogHeader>
            <DialogTitle>{t(($) => $.gitea.connect_dialog_title)}</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.gitea.connect_dialog_description)}
            {baseUrl ? ` ${baseUrl}` : ""}
          </p>
          <div className="space-y-1.5">
            <Label htmlFor="gitea-connect-token">{t(($) => $.gitea.connect_dialog_token_label)}</Label>
            <Input
              id="gitea-connect-token"
              data-testid="gitea-connect-token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              disabled={connecting}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeDialog} disabled={connecting}>
              {t(($) => $.gitea.connect_dialog_cancel)}
            </Button>
            <Button onClick={handleConnect} disabled={connecting || !token.trim()}>
              {connecting ? t(($) => $.gitea.connect_dialog_connecting) : t(($) => $.gitea.connect_dialog_connect)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={disconnectOpen}
        onOpenChange={(v) => {
          if (!v && !disconnecting) setDisconnectOpen(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.gitea.disconnect_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.gitea.disconnect_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={disconnecting}>
              {t(($) => $.gitea.disconnect_confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleDisconnect} disabled={disconnecting}>
              {disconnecting ? t(($) => $.gitea.disconnecting) : t(($) => $.gitea.disconnect_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsTab>
  );
}

function FeatureRow({
  id,
  icon,
  label,
  description,
  checked,
  disabled,
  onCheckedChange,
}: {
  id: string;
  icon: React.ReactNode;
  label: string;
  description: React.ReactNode;
  checked: boolean;
  disabled: boolean;
  onCheckedChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4 px-4 py-3.5">
      <div className="flex items-start gap-3">
        <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground">{icon}</div>
        <div className="space-y-1">
          <Label htmlFor={id} className="text-sm font-medium">
            {label}
          </Label>
          {description}
        </div>
      </div>
      <Switch
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
      />
    </div>
  );
}
