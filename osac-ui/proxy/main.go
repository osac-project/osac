package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/osac/proxy/auth"
	"github.com/osac/proxy/bridge"
	"github.com/osac/proxy/config"
	proxylog "github.com/osac/proxy/log"
	proxymiddleware "github.com/osac/proxy/middleware"
	"github.com/osac/proxy/server"
)

func main() {
	log := proxylog.InitLogs()

	if config.FulfillmentApiUrl == "" {
		log.Fatal("FULFILLMENT_API_URL is required")
	}
	if config.OIDCClientID == "" {
		log.Warn("OIDC_CLIENT_ID is not set — OIDC login endpoints will be unavailable")
	}

	tlsConfig, err := bridge.GetTlsConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to get TLS configuration")
	}

	oidcTLSConfig, err := bridge.GetOIDCTlsConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to get OIDC TLS configuration")
	}

	authHandler := &auth.Handler{
		ClientID:              config.OIDCClientID,
		BaseUIURL:             config.BaseUIURL,
		FulfillmentAPIURL:     config.FulfillmentApiUrl,
		FulfillmentHTTPClient: newHTTPClient(tlsConfig),
		OIDCHTTPClient:        newHTTPClient(oidcTLSConfig),
	}

	compressor := middleware.NewCompressor(5)
	compressor.SetEncoder("br", func(w io.Writer, level int) io.Writer {
		return brotli.NewWriterLevel(w, level)
	})

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Logger)
	router.Use(compressor.Handler)

	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			log.WithError(err).Warn("failed to write /health response")
			return
		}
	})
	router.Get("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ready"}); err != nil {
			log.WithError(err).Warn("failed to write /ready response")
			return
		}
	})

	// Auth endpoints — only available when OIDC_CLIENT_ID is configured.
	if config.OIDCClientID != "" {
		router.Get("/api/login", authHandler.GetLogin)
		router.Post("/api/login", authHandler.PostLogin)
		router.Get("/api/login/info", authHandler.GetLoginInfo)
		router.Get("/api/login/refresh", authHandler.GetLoginRefresh)
		router.Post("/api/logout", authHandler.PostLogout)
	}

	grpcTarget, err := config.FulfillmentGrpcTarget()
	if err != nil {
		log.WithError(err).Fatal("Failed to resolve gRPC target from FULFILLMENT_API_URL")
	}

	// Console WebSocket uses the console-ticket cookie (browser cannot set Authorization).
	// Do not run Auth middleware here: it injects the OIDC access token as Bearer, and
	// console-proxy prefers Authorization over the cookie, causing 401 invalid ticket.
	if config.FulfillmentApiUrl != "" {
		consoleWSHandler, err := bridge.NewConsoleWebSocketProxy(config.FulfillmentApiUrl, tlsConfig)
		if err != nil {
			log.WithError(err).Fatal("Failed to create console WebSocket proxy")
		}
		router.Handle(
			"/api/fulfillment/v1/console_sessions/connect",
			proxymiddleware.ConsoleWebSocketAuth(config.BaseUIURL)(consoleWSHandler),
		)
		log.Info("Console WebSocket proxy enabled")

		router.Post("/api/console-ticket/clear", bridge.NewClearConsoleTicketCookieHandler())
	}

	if grpcTarget != "" {
		var reflectionToken string
		if config.OIDCServiceClientID != "" && config.OIDCServiceClientSecret != "" {
			if config.OIDCIssuerURL == "" {
				log.Fatal("OIDC_ISSUER_URL is required when OIDC_SERVICE_CLIENT_ID and OIDC_SERVICE_CLIENT_SECRET are set")
			}
			tr, err := auth.ClientCredentialsToken(
				config.OIDCIssuerURL,
				config.OIDCServiceClientID,
				config.OIDCServiceClientSecret,
				newHTTPClient(oidcTLSConfig),
			)
			if err != nil {
				log.WithError(err).Fatal("Failed to fetch service account token for gRPC reflection")
			}
			reflectionToken = tr.AccessToken
		}

		connectHandler, err := bridge.NewConnectJSONProxy(grpcTarget, tlsConfig, reflectionToken)
		if err != nil {
			log.WithError(err).Fatal("Failed to create Connect JSON proxy")
		}
		router.Group(func(r chi.Router) {
			r.Use(proxymiddleware.Auth)
			strippedConnect := http.StripPrefix("/api/fulfillment", connectHandler)
			// The console-ticket must never reach the browser as a JS-readable
			// value (see bridge.WrapConsoleSessionCreate) — chi matches this
			// literal path ahead of the /* wildcard below regardless of
			// registration order.
			r.Handle(
				"/api/fulfillment/osac.public.v1.ConsoleSessions/Create",
				bridge.WrapConsoleSessionCreate(strippedConnect),
			)
			r.Handle("/api/fulfillment/*", strippedConnect)
		})
		log.Info("Connect JSON proxy enabled")
	}

	log.Info("Serving SPA static files from /app/public")
	spa := server.SpaHandler{Dir: "/app/public"}
	router.NotFound(spa.ServeHTTP)

	srv := &http.Server{
		Handler:      router,
		Addr:         config.BridgeAddr,
		WriteTimeout: 15 * time.Minute,
		ReadTimeout:  15 * time.Second,
	}

	log.Infof("osac-proxy running at %s (upstream: %s)", config.BridgeAddr, config.FulfillmentApiUrl)
	log.Fatal(srv.ListenAndServe())
}

func newHTTPClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			IdleConnTimeout:     90 * time.Second,
			MaxIdleConnsPerHost: 2,
		},
		Timeout: 15 * time.Second,
	}
}
