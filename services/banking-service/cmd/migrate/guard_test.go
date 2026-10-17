package main

import (
	"testing"

	"github.com/qingyingjin/banking-service/internal/config"
)

func TestMigrationTargetGuard(t *testing.T) {
	for _, tc := range []struct {
		name, driver, database, expected string
		testOnly, reject                 bool
	}{
		{"isolated PG", "postgres", "finance_core_test", "finance_core_test", true, false},
		{"isolated MySQL", "mysql", "finance_core_test", "finance_core_test", true, false},
		{"environment points at main", "postgres", "finance", "finance_core_test", true, true},
		{"test-only forbids main even when expected", "postgres", "finance", "finance", true, true},
		{"explicit main deployment", "postgres", "finance", "finance", false, false},
		{"existing CLI compatibility", "mysql", "finance", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: tc.driver, PostgresDatabase: tc.database, MySQLDatabase: tc.database}
			if err := validateMigrationTarget(cfg, tc.expected, tc.testOnly); (err != nil) != tc.reject {
				t.Fatalf("target guard rejection=%v, want=%v", err != nil, tc.reject)
			}
		})
	}
}
