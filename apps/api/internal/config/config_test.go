package config

import (
	"strings"
	"testing"
	"time"
)

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("NERAE_ENVIRONMENT", "test")
	t.Setenv("NERAE_HTTP_ADDR", "127.0.0.1:18080")
	t.Setenv("NERAE_MYSQL_DSN", "fixture-user:fixture-secret@tcp(127.0.0.1:1)/nerae_test")
	t.Setenv("NERAE_REDIS_URL", "redis://:fixture-redis-secret@127.0.0.1:1/1")
	t.Setenv("NERAE_HEALTH_TIMEOUT", "")
	t.Setenv("NERAE_SHUTDOWN_TIMEOUT", "")
}

func TestLoadValidConfiguration(t *testing.T) {
	setValidEnvironment(t)
	cfg, err := Load(API)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "test" || cfg.HealthTimeout != 2*time.Second || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected non-secret configuration: environment=%s health=%s shutdown=%s", cfg.Environment, cfg.HealthTimeout, cfg.ShutdownTimeout)
	}
	t.Setenv("NERAE_HTTP_ADDR", "")
	if _, err := Load(Worker); err != nil {
		t.Fatalf("worker does not use an HTTP listener: %v", err)
	}
}

func TestLoadRejectsInvalidSettingsWithoutLeakingValues(t *testing.T) {
	cases := []struct{ name, key, value, setting string }{
		{"missing environment", "NERAE_ENVIRONMENT", "", "NERAE_ENVIRONMENT"},
		{"bad environment", "NERAE_ENVIRONMENT", "unknown", "NERAE_ENVIRONMENT"},
		{"missing address", "NERAE_HTTP_ADDR", "", "NERAE_HTTP_ADDR"},
		{"port out of range", "NERAE_HTTP_ADDR", "127.0.0.1:65536", "NERAE_HTTP_ADDR"},
		{"zero port", "NERAE_HTTP_ADDR", "127.0.0.1:0", "NERAE_HTTP_ADDR"},
		{"missing mysql", "NERAE_MYSQL_DSN", "", "NERAE_MYSQL_DSN"},
		{"invalid mysql", "NERAE_MYSQL_DSN", "fixture-private-value", "NERAE_MYSQL_DSN"},
		{"mysql without database", "NERAE_MYSQL_DSN", "test@tcp(127.0.0.1:1)/", "NERAE_MYSQL_DSN"},
		{"missing redis", "NERAE_REDIS_URL", "", "NERAE_REDIS_URL"},
		{"invalid redis", "NERAE_REDIS_URL", "https://fixture-private-value", "NERAE_REDIS_URL"},
		{"invalid health timeout", "NERAE_HEALTH_TIMEOUT", "50ms", "NERAE_HEALTH_TIMEOUT"},
		{"invalid shutdown timeout", "NERAE_SHUTDOWN_TIMEOUT", "3m", "NERAE_SHUTDOWN_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load(API)
			if err == nil || !strings.Contains(err.Error(), tc.setting) {
				t.Fatalf("expected an error naming %s", tc.setting)
			}
			if tc.value != "" && strings.Contains(err.Error(), tc.value) {
				t.Fatal("configuration error leaked a value")
			}
			if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "fixture-redis-secret") {
				t.Fatal("configuration error leaked a credential")
			}
		})
	}
	if _, err := Load(Process("invalid")); err == nil {
		t.Fatal("unknown process accepted")
	}
}

func TestWorkerRejectsInvalidDatabaseConfigurationWithoutLeakingIt(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("NERAE_HTTP_ADDR", "")
	t.Setenv("NERAE_MYSQL_DSN", "worker-private-value")
	_, err := Load(Worker)
	if err == nil || !strings.Contains(err.Error(), "NERAE_MYSQL_DSN") {
		t.Fatal("worker accepted invalid database configuration")
	}
	if strings.Contains(err.Error(), "worker-private-value") {
		t.Fatal("worker configuration error leaked a value")
	}
}
