// Package config reads and validates process configuration without logging it.
package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

type Process string

const (
	API    Process = "api"
	Worker Process = "worker"
)

type Config struct {
	Environment     string
	HTTPAddress     string
	MySQLDSN        string
	RedisURL        string
	HealthTimeout   time.Duration
	ShutdownTimeout time.Duration
}

// Load reads environment variables. Errors name only the invalid setting and
// never include its value; callers must not log the returned Config.
func Load(process Process) (Config, error) {
	if process != API && process != Worker {
		return Config{}, errors.New("invalid process")
	}
	v := viper.New()
	v.SetEnvPrefix("NERAE")
	v.AutomaticEnv()

	cfg := Config{
		Environment: v.GetString("environment"),
		HTTPAddress: v.GetString("http_addr"),
		MySQLDSN:    v.GetString("mysql_dsn"),
		RedisURL:    v.GetString("redis_url"),
	}
	switch cfg.Environment {
	case "development", "test", "production":
	default:
		return Config{}, errors.New("NERAE_ENVIRONMENT must be development, test or production")
	}
	if process == API {
		_, port, err := net.SplitHostPort(cfg.HTTPAddress)
		if err != nil {
			return Config{}, errors.New("NERAE_HTTP_ADDR must be a valid host:port")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, errors.New("NERAE_HTTP_ADDR must use a port from 1 to 65535")
		}
	}
	if cfg.MySQLDSN == "" {
		return Config{}, errors.New("NERAE_MYSQL_DSN is required")
	}
	mysqlConfig, err := mysql.ParseDSN(cfg.MySQLDSN)
	if err != nil || mysqlConfig.User == "" || mysqlConfig.DBName == "" {
		return Config{}, errors.New("NERAE_MYSQL_DSN must specify a valid user and database")
	}
	if cfg.RedisURL == "" {
		return Config{}, errors.New("NERAE_REDIS_URL is required")
	}
	if _, err := redis.ParseURL(cfg.RedisURL); err != nil {
		return Config{}, errors.New("NERAE_REDIS_URL must be a valid redis or rediss URL")
	}
	cfg.HealthTimeout, err = duration(v.GetString("health_timeout"), 2*time.Second, time.Second, 30*time.Second)
	if err != nil {
		return Config{}, errors.New("NERAE_HEALTH_TIMEOUT must be between 1s and 30s")
	}
	cfg.ShutdownTimeout, err = duration(v.GetString("shutdown_timeout"), 10*time.Second, time.Second, 2*time.Minute)
	if err != nil {
		return Config{}, errors.New("NERAE_SHUTDOWN_TIMEOUT must be between 1s and 2m")
	}
	return cfg, nil
}

func duration(value string, fallback, min, max time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("duration outside allowed range")
	}
	return parsed, nil
}
