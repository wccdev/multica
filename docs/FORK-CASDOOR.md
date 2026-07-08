# Fork patch: Casdoor OIDC SSO (gcoreinc)
#
# This fork adds Casdoor login alongside upstream Google OAuth. Google code is
# untouched; Casdoor lives in separate files/paths for easier upstream merges.
#
# After `git pull upstream main`, re-check these files:
#
# Backend (usually low conflict):
#   server/internal/handler/casdoor_login.go     — fork-only, new file
#   server/internal/handler/config.go              — small additive fields
#   server/cmd/server/router.go                    — +1 route line
#
# Shared packages (moderate conflict):
#   packages/core/config/index.ts
#   packages/core/api/schemas.ts
#   packages/core/api/client.ts
#   packages/core/auth/store.ts
#   packages/core/platform/auth-initializer.tsx
#
# Web app (fork-specific UI):
#   apps/web/features/auth/casdoor-login-button.tsx  — fork-only, new file
#   apps/web/app/(auth)/login/page.tsx
#   apps/web/app/auth/callback/page.tsx
#
# Upstream files intentionally NOT modified:
#   packages/views/auth/login-page.tsx
#   server/internal/handler/auth.go (GoogleLogin)
#
# Configuration (.env):
#   CASDOOR_ENDPOINT=https://casdoor.gcoreinc.com
#   CASDOOR_CLIENT_ID=<from Casdoor application>
#   CASDOOR_CLIENT_SECRET=<from Casdoor application>
#   CASDOOR_REDIRECT_URI=https://<your-multica-app>/auth/callback  # optional
#
# Casdoor application setup:
#   1. Create an OIDC application in Casdoor
#   2. Redirect URL: https://<your-multica-frontend>/auth/callback
#   3. Copy Client ID and Client Secret into .env
#   4. Ensure scopes include openid profile email
#   5. Restart Multica backend (config is read at runtime for /api/config)
#
# To hide Google and use Casdoor only: leave GOOGLE_CLIENT_ID empty.
#
# CI: `.github/workflows/dockerhub-amd64.yml` builds linux/amd64 images and
# pushes to Docker Hub when this repo is `wccdev/multica`. Configure secrets:
#   DOCKER_USERNAME, DOCKER_PASSWORD
