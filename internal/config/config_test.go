package config

import (
	"errors"
	"testing"
)

func TestLoadAuthValidation(t *testing.T) {
	cases := []struct {
		name                 string
		token                string
		allowUnauthenticated string
		wantErr              error
		wantToken            string
		wantAllow            bool
	}{
		{
			name:                 "no token, no opt-out fails closed",
			token:                "",
			allowUnauthenticated: "",
			wantErr:              ErrAuthTokenRequired,
		},
		{
			name:                 "token set",
			token:                "secret",
			allowUnauthenticated: "",
			wantToken:            "secret",
		},
		{
			name:                 "no token, opt-out enabled",
			token:                "",
			allowUnauthenticated: "true",
			wantAllow:            true,
		},
		{
			name:                 "token set and opt-out enabled",
			token:                "secret",
			allowUnauthenticated: "true",
			wantToken:            "secret",
			wantAllow:            true,
		},
		{
			name:                 "opt-out non-true value fails closed",
			token:                "",
			allowUnauthenticated: "1",
			wantErr:              ErrAuthTokenRequired,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AuthTokenEnv, tc.token)
			t.Setenv(AuthAllowUnauthenticatedEnv, tc.allowUnauthenticated)

			cfg, err := Load()

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("unexpected error: got %v, want %v", err, tc.wantErr)
				}
				if cfg != nil {
					t.Errorf("expected nil config on error, got %+v", cfg)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
			if cfg.AuthToken != tc.wantToken {
				t.Errorf("unexpected AuthToken: got %q, want %q", cfg.AuthToken, tc.wantToken)
			}
			if cfg.AuthAllowUnauthenticated != tc.wantAllow {
				t.Errorf("unexpected AuthAllowUnauthenticated: got %v, want %v", cfg.AuthAllowUnauthenticated, tc.wantAllow)
			}
		})
	}
}
