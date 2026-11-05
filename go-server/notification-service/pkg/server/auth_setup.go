package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/example/notification-service/pkg/auth"
)

// initAuth builds the account service, creates the first admin when asked to,
// and sets up single sign-on when it is configured.
func (s *Server) initAuth(ctx context.Context) error {
	var store auth.Store = auth.NewMemoryStore()
	if s.sqlDB != nil {
		store = auth.NewPostgresStore(s.sqlDB)
	}
	s.auth = auth.NewService(store, s.cfg.AuthSessionTTL)

	if s.cfg.AuthBootstrapEmail != "" {
		made, err := s.auth.Bootstrap(ctx, s.cfg.AuthBootstrapEmail, s.cfg.AuthBootstrapPassword)
		if err != nil {
			return fmt.Errorf("AUTH_BOOTSTRAP: %w", err)
		}
		if made {
			s.logger.Info("created the first admin account from AUTH_BOOTSTRAP_EMAIL")
		}
	}

	s.authSecret = []byte(s.cfg.AuthSecret)
	if len(s.authSecret) == 0 {
		s.authSecret = randomBytes(32)
		if s.cfg.OIDCClientID != "" {
			s.logger.Warn("AUTH_SECRET is not set; single sign-on in progress will fail if the service restarts or several replicas run")
		}
	}

	oidc, err := s.buildOIDC()
	if err != nil {
		return err
	}
	s.oidc = oidc
	return nil
}

// buildOIDC returns nil when single sign-on is off. Microsoft Entra ID is a
// shortcut (ENTRA_TENANT_ID); any other OpenID Connect provider uses OIDC_ISSUER.
func (s *Server) buildOIDC() (*auth.OIDC, error) {
	cfg := s.cfg
	issuer, name := cfg.OIDCIssuer, cfg.OIDCName
	if issuer == "" && cfg.EntraTenantID != "" {
		issuer = auth.EntraIssuer(cfg.EntraTenantID)
		if name == "" {
			name = "Microsoft"
		}
	}
	if issuer == "" && cfg.OIDCClientID == "" {
		return nil, nil
	}
	if cfg.PublicURL == "" {
		return nil, errors.New("single sign-on needs PUBLIC_URL, the service's public address, to build the callback")
	}
	o, err := auth.NewOIDC(auth.OIDCConfig{
		Name: name, Issuer: issuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
		RedirectURL: cfg.PublicURL + authPrefix + "/oidc/callback",
	})
	if err != nil {
		return nil, fmt.Errorf("single sign-on: %w", err)
	}
	s.logger.Info("single sign-on enabled", "provider", o.Name(), "callback", cfg.PublicURL+authPrefix+"/oidc/callback")
	return o, nil
}
