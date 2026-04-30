// Copyright (c) 2026 Lark Technologies Pte. Ltd.
// SPDX-License-Identifier: MIT

package auth

import (
	"net/url"
	"strings"
	"testing"

	larkauth "github.com/larksuite/cli/internal/auth"
	"github.com/larksuite/cli/internal/cmdutil"
	"github.com/larksuite/cli/internal/core"
	"github.com/larksuite/cli/internal/httpmock"
	"github.com/zalando/go-keyring"
)

func TestAuthLogoutRun_RevokesTokenAndClearsLocalState(t *testing.T) {
	keyring.MockInit()
	setupLoginConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	multi := &core.MultiAppConfig{
		CurrentApp: "default",
		Apps: []core.AppConfig{
			{
				Name:      "default",
				AppId:     "cli_test",
				AppSecret: core.PlainSecret("secret"),
				Brand:     core.BrandFeishu,
				Users:     []core.AppUser{{UserOpenId: "ou_user", UserName: "tester"}},
			},
		},
	}
	if err := core.SaveMultiAppConfig(multi); err != nil {
		t.Fatalf("SaveMultiAppConfig() error = %v", err)
	}
	if err := larkauth.SetStoredToken(&larkauth.StoredUAToken{
		AppId:       "cli_test",
		UserOpenId:  "ou_user",
		AccessToken: "user-access-token",
	}); err != nil {
		t.Fatalf("SetStoredToken() error = %v", err)
	}

	f, _, stderr, reg := cmdutil.TestFactory(t, &core.CliConfig{
		ProfileName: "default",
		AppID:       "cli_test",
		AppSecret:   "secret",
		Brand:       core.BrandFeishu,
	})

	reg.Register(&httpmock.Stub{
		Method: "POST",
		URL:    larkauth.PathOAuthRevoke,
		Body:   map[string]interface{}{"code": 0},
		BodyFilter: func(body []byte) bool {
			values, err := url.ParseQuery(string(body))
			if err != nil {
				return false
			}
			return values.Get("client_id") == "cli_test" &&
				values.Get("client_secret") == "secret" &&
				values.Get("token") == "user-access-token" &&
				values.Get("token_type_hint") == "access_token"
		},
	})

	if err := authLogoutRun(&LogoutOptions{Factory: f}); err != nil {
		t.Fatalf("authLogoutRun() error = %v", err)
	}

	if got := stderr.String(); !strings.Contains(got, "Logged out") {
		t.Fatalf("stderr = %q, want Logged out", got)
	}
	if got := larkauth.GetStoredToken("cli_test", "ou_user"); got != nil {
		t.Fatalf("expected stored token removed, got %#v", got)
	}
	saved, err := core.LoadMultiAppConfig()
	if err != nil {
		t.Fatalf("LoadMultiAppConfig() error = %v", err)
	}
	if len(saved.Apps) != 1 || len(saved.Apps[0].Users) != 0 {
		t.Fatalf("expected users cleared, got %#v", saved.Apps)
	}
}

func TestAuthLogoutRun_RevokeFailureStillClearsLocalState(t *testing.T) {
	keyring.MockInit()
	setupLoginConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	multi := &core.MultiAppConfig{
		CurrentApp: "default",
		Apps: []core.AppConfig{
			{
				Name:      "default",
				AppId:     "cli_test",
				AppSecret: core.PlainSecret("secret"),
				Brand:     core.BrandFeishu,
				Users:     []core.AppUser{{UserOpenId: "ou_user", UserName: "tester"}},
			},
		},
	}
	if err := core.SaveMultiAppConfig(multi); err != nil {
		t.Fatalf("SaveMultiAppConfig() error = %v", err)
	}
	if err := larkauth.SetStoredToken(&larkauth.StoredUAToken{
		AppId:       "cli_test",
		UserOpenId:  "ou_user",
		AccessToken: "user-access-token",
	}); err != nil {
		t.Fatalf("SetStoredToken() error = %v", err)
	}

	f, _, stderr, reg := cmdutil.TestFactory(t, &core.CliConfig{
		ProfileName: "default",
		AppID:       "cli_test",
		AppSecret:   "secret",
		Brand:       core.BrandFeishu,
	})

	reg.Register(&httpmock.Stub{
		Method: "POST",
		URL:    larkauth.PathOAuthRevoke,
		Status: 500,
		Body:   map[string]interface{}{"error": "server_error"},
	})

	if err := authLogoutRun(&LogoutOptions{Factory: f}); err != nil {
		t.Fatalf("authLogoutRun() error = %v", err)
	}

	gotErr := stderr.String()
	if !strings.Contains(gotErr, "failed to revoke token for ou_user") {
		t.Fatalf("stderr = %q, want revoke warning", gotErr)
	}
	if !strings.Contains(gotErr, "Logged out") {
		t.Fatalf("stderr = %q, want Logged out", gotErr)
	}
	if got := larkauth.GetStoredToken("cli_test", "ou_user"); got != nil {
		t.Fatalf("expected stored token removed, got %#v", got)
	}
	saved, err := core.LoadMultiAppConfig()
	if err != nil {
		t.Fatalf("LoadMultiAppConfig() error = %v", err)
	}
	if len(saved.Apps) != 1 || len(saved.Apps[0].Users) != 0 {
		t.Fatalf("expected users cleared, got %#v", saved.Apps)
	}
}
