package main

import (
	"errors"
	"strings"

	"github.com/qingyingjin/banking-service/internal/config"
)

// Resolve and verify the actual target before opening any connection. This also
// catches inherited environment values overriding a selected private env file.
func validateMigrationTarget(cfg config.Config, expected string, testOnly bool) error {
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if expected != "" && name != expected {
		return errors.New("resolved migration database differs from the explicitly expected target")
	}
	if testOnly && (expected == "" || !strings.HasSuffix(name, "_test")) {
		return errors.New("test migration requires an explicit matching isolated *_test database")
	}
	return nil
}
