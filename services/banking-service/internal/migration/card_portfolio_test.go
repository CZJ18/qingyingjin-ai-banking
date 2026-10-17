package migration

import (
	"strings"
	"testing"
)

func TestCardPortfolioMigrationIsAdditive(t *testing.T) {
	if CurrentVersion < 14 {
		t.Fatal("schema 14 required")
	}
	sql := strings.TrimSpace(cardPortfolioSQL)
	if sql != "ALTER TABLE banking_users ADD COLUMN card_portfolio TEXT NULL;" {
		t.Fatalf("unexpected migration: %s", sql)
	}
}
