package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"nerae/api/internal/config"
)

type probe func(context.Context) error

func newRouter(mysqlPing, redisPing probe, timeout time.Duration) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// Do not log request headers, DSNs, Redis URLs or panic payloads.
	router.Use(func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	})
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		mysqlErr := mysqlPing(ctx)
		redisErr := redisPing(ctx)
		if mysqlErr != nil || redisErr != nil || ctx.Err() != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	return router
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if ctx == nil || logger == nil || cfg.HealthTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		return errors.New("API configuration unavailable")
	}
	business, err := businessMode(cfg.Environment, os.Getenv("NERAE_API_ENABLE_BUSINESS"))
	if err != nil {
		return errors.New("API configuration unavailable")
	}
	var settings apiBusinessSettings
	if business {
		settings, err = loadAPISettings()
		if err != nil {
			return errors.New("API business configuration unavailable")
		}
	}
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return errors.New("cannot configure MySQL client")
	}
	defer db.Close()
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return errors.New("cannot configure Redis client")
	}
	if business && !validBusinessNetwork(cfg.HTTPAddress, cfg.MySQLDSN, redisOptions) {
		return errors.New("API business network configuration unavailable")
	}
	// Health checks must respect the request deadline, including connection
	// setup and pool waits. A failed dependency is not retried as healthy.
	redisOptions.ContextTimeoutEnabled = true
	redisOptions.DialTimeout = cfg.HealthTimeout
	redisOptions.ReadTimeout = cfg.HealthTimeout
	redisOptions.WriteTimeout = cfg.HealthTimeout
	redisOptions.PoolTimeout = cfg.HealthTimeout
	redisOptions.MaxRetries = -1
	redisOptions.DialerRetries = 1
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()

	router := newRouter(db.PingContext, func(ctx context.Context) error {
		return redisClient.Ping(ctx).Err()
	}, cfg.HealthTimeout)
	if business {
		startup, cancel := context.WithTimeout(ctx, cfg.HealthTimeout)
		handler, buildErr := buildBusinessHandler(startup, db, redisClient, settings.keys, settings.policy, settings.proxies)
		cancel()
		if buildErr != nil || handler == nil {
			return errors.New("API business startup unavailable")
		}
		router.NoRoute(gin.WrapH(handler))
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return errors.New("cannot start HTTP listener")
	}
	var serverHandler http.Handler = router
	if business {
		// Wrap the whole router so future business routes cannot bypass proxy
		// authentication. The proxy must overwrite both trusted headers.
		serverHandler = proxyBoundary(settings.proxyCredential, router)
	}
	server := &http.Server{Handler: serverHandler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	logger.Info("api process started")
	select {
	case err := <-done:
		if err != nil {
			return errors.New("HTTP server stopped unexpectedly")
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return errors.New("HTTP shutdown failed")
		}
		if err := <-done; err != nil {
			return errors.New("HTTP server stopped unexpectedly")
		}
		logger.Info("api process stopped")
		return nil
	}
}

const proxyCredentialHeader = "X-Nerae-Proxy-Credential"

// The exact health GETs are dependency probes, not business endpoints.
func proxyBoundary(secret []byte, next http.Handler) http.Handler {
	guarded := authenticateProxy(secret, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL != nil && r.URL.RawPath == "" && r.URL.RawQuery == "" && !r.URL.ForceQuery &&
			(r.URL.Path == "/health/live" || r.URL.Path == "/health/ready") {
			removeProxyCredential(r.Header)
			next.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

// authenticateProxy is a service-to-service boundary, not user auth. A
// separate CSPRNG credential must be provisioned privately to the proxy and API.
// Its loopback hop is plaintext; the host and proxy must remain trusted.
func authenticateProxy(secret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values []string
		count := 0
		for name, candidate := range r.Header {
			if strings.EqualFold(name, proxyCredentialHeader) {
				count++
				values = candidate
			}
		}
		var supplied []byte
		if count == 1 && len(values) == 1 && len(values[0]) == 43 {
			supplied, _ = base64.RawURLEncoding.Strict().DecodeString(values[0])
			if base64.RawURLEncoding.EncodeToString(supplied) != values[0] {
				supplied = nil
			}
		}
		if len(secret) != 32 || len(supplied) != 32 || subtle.ConstantTimeCompare(secret, supplied) != 1 {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		removeProxyCredential(r.Header)
		next.ServeHTTP(w, r)
	})
}

func removeProxyCredential(headers http.Header) {
	for name := range headers {
		if strings.EqualFold(name, proxyCredentialHeader) {
			delete(headers, name)
		}
	}
}

// Health-only mode remains available for the pre-existing local process
// checks. Production must opt into business mode and cannot silently report a
// ready skeleton as an application service.
func businessMode(environment, value string) (bool, error) {
	if environment != "development" && environment != "test" && environment != "production" {
		return false, errAPIConfig
	}
	switch value {
	case "true":
		return true, nil
	case "", "false":
		if environment == "production" {
			return false, errAPIConfig
		}
		return false, nil
	default:
		return false, errAPIConfig
	}
}

func validBusinessNetwork(address, dsn string, redisOptions *redis.Options) bool {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil || endpoint.Port() == 0 {
		return false
	}
	ip := endpoint.Addr()
	if ip.Zone() != "" || !ip.Unmap().IsLoopback() {
		return false
	}
	mysqlConfig, err := mysql.ParseDSN(dsn)
	if err != nil || mysqlConfig.User == "" || mysqlConfig.DBName == "" || !mysqlConfig.ParseTime ||
		mysqlConfig.Loc == nil || mysqlConfig.Loc.String() != "UTC" || mysqlConfig.AllowFallbackToPlaintext {
		return false
	}
	if mysqlConfig.Net == "unix" {
		if !filepath.IsAbs(mysqlConfig.Addr) {
			return false
		}
	} else if mysqlConfig.Net != "tcp" || mysqlConfig.TLSConfig != "true" {
		return false
	}
	if redisOptions == nil || redisOptions.Addr == "" {
		return false
	}
	if redisOptions.Network == "unix" {
		return filepath.IsAbs(redisOptions.Addr)
	}
	return redisOptions.Network == "tcp" && redisOptions.Password != "" && redisOptions.TLSConfig != nil &&
		!redisOptions.TLSConfig.InsecureSkipVerify && redisOptions.TLSConfig.ServerName != "" &&
		redisOptions.TLSConfig.MinVersion >= tls.VersionTLS12
}
