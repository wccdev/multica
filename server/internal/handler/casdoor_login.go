package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type CasdoorLoginRequest struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

type casdoorUserInfo struct {
	Email             string `json:"email"`
	Name              string `json:"name"`
	DisplayName       string `json:"displayName"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
	Avatar            string `json:"avatar"`
}

func casdoorEndpointFromEnv() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("CASDOOR_ENDPOINT")), "/")
}

func (h *Handler) CasdoorLogin(w http.ResponseWriter, r *http.Request) {
	var req CasdoorLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	endpoint := casdoorEndpointFromEnv()
	clientID := os.Getenv("CASDOOR_CLIENT_ID")
	clientSecret := os.Getenv("CASDOOR_CLIENT_SECRET")
	if endpoint == "" || clientID == "" || clientSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Casdoor login is not configured")
		return
	}

	redirectURI := req.RedirectURI
	if redirectURI == "" {
		redirectURI = os.Getenv("CASDOOR_REDIRECT_URI")
	}

	tokenResp, err := http.PostForm(endpoint+"/api/login/oauth/access_token", url.Values{
		"code":          {req.Code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		slog.Error("casdoor oauth token exchange failed", "error", err)
		writeError(w, http.StatusBadGateway, "failed to exchange code with Casdoor")
		return
	}
	defer tokenResp.Body.Close()

	tokenBody, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read Casdoor token response")
		return
	}

	if tokenResp.StatusCode != http.StatusOK {
		slog.Error("casdoor oauth token exchange returned error", "status", tokenResp.StatusCode, "body", string(tokenBody))
		writeError(w, http.StatusBadRequest, "failed to exchange code with Casdoor")
		return
	}

	var oToken oauthTokenResponse
	if err := json.Unmarshal(tokenBody, &oToken); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse Casdoor token response")
		return
	}

	userInfoReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint+"/api/userinfo", nil)
	if err != nil {
		slog.Error("failed to create casdoor userinfo request", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	userInfoReq.Header.Set("Authorization", "Bearer "+oToken.AccessToken)

	userInfoResp, err := http.DefaultClient.Do(userInfoReq)
	if err != nil {
		slog.Error("casdoor userinfo fetch failed", "error", err)
		writeError(w, http.StatusBadGateway, "failed to fetch user info from Casdoor")
		return
	}
	defer userInfoResp.Body.Close()

	if userInfoResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(userInfoResp.Body)
		slog.Error("casdoor userinfo returned error", "status", userInfoResp.StatusCode, "body", string(body))
		writeError(w, http.StatusBadGateway, "failed to fetch user info from Casdoor")
		return
	}

	var cUser casdoorUserInfo
	if err := json.NewDecoder(userInfoResp.Body).Decode(&cUser); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse Casdoor user info")
		return
	}

	email := strings.ToLower(strings.TrimSpace(cUser.Email))
	if email == "" {
		writeError(w, http.StatusBadRequest, "Casdoor account has no email")
		return
	}

	user, isNew, err := h.findOrCreateUser(r.Context(), email)
	if err != nil {
		var signupErr SignupError
		if errors.As(err, &signupErr) {
			writeError(w, http.StatusForbidden, signupErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	if isNew {
		evt := analytics.Signup(uuidToString(user.ID), user.Email, signupSourceFromRequest(r))
		evt.Properties["auth_method"] = "casdoor"
		obsmetrics.RecordEvent(h.Analytics, h.Metrics, evt)
	}

	displayName := firstNonEmpty(cUser.Name, cUser.DisplayName, cUser.PreferredUsername)
	avatarURL := firstNonEmpty(cUser.Picture, cUser.Avatar)

	needsUpdate := false
	newName := user.Name
	newAvatar := user.AvatarUrl

	if displayName != "" && user.Name == strings.Split(email, "@")[0] {
		newName = displayName
		needsUpdate = true
	}
	if avatarURL != "" && !user.AvatarUrl.Valid {
		newAvatar = pgtype.Text{String: avatarURL, Valid: true}
		needsUpdate = true
	}

	if needsUpdate {
		updated, err := h.Queries.UpdateUser(r.Context(), db.UpdateUserParams{
			ID:        user.ID,
			Name:      newName,
			AvatarUrl: newAvatar,
		})
		if err == nil {
			user = updated
		}
	}

	tokenString, err := h.issueJWT(user)
	if err != nil {
		slog.Warn("casdoor login failed", append(logger.RequestAttrs(r), "error", err, "email", email)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set auth cookies", "error", err)
	}

	if h.CFSigner != nil {
		for _, cookie := range h.CFSigner.SignedCookies(time.Now().Add(72 * time.Hour)) {
			http.SetCookie(w, cookie)
		}
	}

	slog.Info("user logged in via casdoor", append(logger.RequestAttrs(r), "user_id", uuidToString(user.ID), "email", user.Email)...)
	writeJSON(w, http.StatusOK, LoginResponse{
		Token: tokenString,
		User:  userToResponse(user),
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
