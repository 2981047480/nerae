package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
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
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return errors.New("cannot configure MySQL client")
	}
	defer db.Close()
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return errors.New("cannot configure Redis client")
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
	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return errors.New("cannot start HTTP listener")
	}
	server := &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}
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
