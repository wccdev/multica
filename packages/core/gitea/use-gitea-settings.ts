"use client";

import { useMemo } from "react";
import { useCurrentWorkspace } from "../paths";
import { deriveGiteaSettings, type GiteaSettings } from "./settings";

/**
 * Reads the Gitea feature flags off the current workspace's settings JSONB,
 * mirroring useGitHubSettings.
 */
export function useGiteaSettings(): GiteaSettings {
  const workspace = useCurrentWorkspace();
  return useMemo(() => deriveGiteaSettings(workspace), [workspace]);
}
