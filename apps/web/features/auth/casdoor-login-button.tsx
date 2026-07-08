"use client";

import { Button } from "@multica/ui/components/ui/button";
import { useConfigStore } from "@multica/core/config";
import { useT } from "@multica/views/i18n";

interface CasdoorLoginButtonProps {
  /** Opaque OAuth state (platform/desktop, next, CLI callback, provider:casdoor). */
  state?: string;
  disabled?: boolean;
  /** When true, render the "or" divider above the button. */
  showDivider?: boolean;
}

function buildCasdoorAuthorizeURL(
  endpoint: string,
  clientId: string,
  redirectUri: string,
  state?: string,
) {
  const params = new URLSearchParams({
    client_id: clientId,
    redirect_uri: redirectUri,
    response_type: "code",
    scope: "openid profile email",
  });
  if (state) params.set("state", state);
  return `${endpoint.replace(/\/+$/, "")}/login/oauth/authorize?${params}`;
}

export function CasdoorLoginButton({
  state,
  disabled = false,
  showDivider = true,
}: CasdoorLoginButtonProps) {
  const { t } = useT("auth");
  const clientId = useConfigStore((s) => s.casdoorClientId);
  const endpoint = useConfigStore((s) => s.casdoorEndpoint);

  if (!clientId || !endpoint) return null;

  const handleClick = () => {
    const redirectUri = `${window.location.origin}/auth/callback`;
    window.location.href = buildCasdoorAuthorizeURL(
      endpoint,
      clientId,
      redirectUri,
      state,
    );
  };

  return (
    <>
      {showDivider && (
        <div className="relative w-full">
          <div className="absolute inset-0 flex items-center">
            <span className="w-full border-t" />
          </div>
          <div className="relative flex justify-center text-xs uppercase">
            <span className="bg-card px-2 text-muted-foreground">
              {t(($) => $.signin.divider)}
            </span>
          </div>
        </div>
      )}
      <Button
        type="button"
        variant="outline"
        className="w-full"
        size="lg"
        onClick={handleClick}
        disabled={disabled}
      >
        <img
          src="/casdoor-favicon.svg"
          alt=""
          className="mr-2 h-4 w-4"
        />
        {t(($) => $.signin.casdoor)}
      </Button>
    </>
  );
}

/** Append provider marker so /auth/callback can route to /auth/casdoor. */
export function withCasdoorProviderState(state?: string) {
  const parts = ["provider:casdoor", state].filter(Boolean);
  return parts.join(",") || undefined;
}
