package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthRoutesReflectDependencyChecks(t *testing.T) {
	secretError := errors.New("fixture-private-dsn")
	cases := []struct {
		name               string
		mysqlErr, redisErr error
		want               int
	}{
		{"all healthy", nil, nil, http.StatusOK},
		{"mysql unavailable", secretError, nil, http.StatusServiceUnavailable},
		{"redis unavailable", nil, secretError, http.StatusServiceUnavailable},
		{"both unavailable", secretError, secretError, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mysqlCalls, redisCalls := 0, 0
			router := newRouter(func(context.Context) error { mysqlCalls++; return tc.mysqlErr }, func(context.Context) error { redisCalls++; return tc.redisErr }, time.Second)
			live := httptest.NewRecorder()
			router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health/live", nil))
			if live.Code != http.StatusOK || mysqlCalls != 0 || redisCalls != 0 {
				t.Fatal("liveness must not require dependencies")
			}
			ready := httptest.NewRecorder()
			router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if ready.Code != tc.want || mysqlCalls != 1 || redisCalls != 1 {
				t.Fatalf("readiness=%d; checks=%d/%d", ready.Code, mysqlCalls, redisCalls)
			}
			if strings.Contains(ready.Body.String(), secretError.Error()) {
				t.Fatal("dependency error leaked in response")
			}
		})
	}
}

func TestHealthCheckTimeoutAndPanicAreFailures(t *testing.T) {
	router := newRouter(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error { return nil }, 10*time.Millisecond)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("timeout status=%d", result.Code)
	}

	for _, panicAt := range []string{"mysql", "redis"} {
		t.Run(panicAt+" panic", func(t *testing.T) {
			mysqlPing := func(context.Context) error {
				if panicAt == "mysql" {
					panic("fixture-secret")
				}
				return nil
			}
			redisPing := func(context.Context) error {
				if panicAt == "redis" {
					panic("fixture-secret")
				}
				return nil
			}
			router := newRouter(mysqlPing, redisPing, time.Second)
			result := httptest.NewRecorder()
			router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if result.Code != http.StatusInternalServerError || strings.Contains(result.Body.String(), "fixture-secret") {
				t.Fatal("panic was not safely handled")
			}
		})
	}
}

func TestCancelledRequestCannotReportReadiness(t *testing.T) {
	router := newRouter(func(context.Context) error { return nil }, func(context.Context) error { return nil }, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil).WithContext(ctx)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancelled request reported status %d", result.Code)
	}
}
