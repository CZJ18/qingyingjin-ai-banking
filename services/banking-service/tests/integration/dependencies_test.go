//go:build integration

package integration

import (
	"context"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRealMySQLRedisAndHTTPReadiness(t *testing.T) {
	if path := os.Getenv("BANKING_TEST_ENV_FILE"); path != "" {
		if err := godotenv.Load(path); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal("cannot initialize connections")
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if conn.CheckMySQL(ctx) != nil {
		t.Fatal("real MySQL readiness failed")
	}
	if conn.CheckRedis(ctx) != nil {
		t.Fatal("real Redis readiness failed")
	}
	sqlConn, err := conn.SQL.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlConn.Close()
	if _, err := sqlConn.ExecContext(ctx, "CREATE TEMPORARY TABLE banking_module1_probe (id BIGINT PRIMARY KEY, value VARCHAR(32) NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlConn.ExecContext(ctx, "INSERT INTO banking_module1_probe VALUES (1, 'persisted-on-connection')"); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := sqlConn.QueryRowContext(ctx, "SELECT value FROM banking_module1_probe WHERE id = 1").Scan(&value); err != nil || value != "persisted-on-connection" {
		t.Fatal("real MySQL roundtrip failed")
	}
	key := "banking:integration:" + uuid.NewString()
	if err := conn.Redis.Set(ctx, key, "module1", time.Minute).Err(); err != nil {
		t.Fatal("real Redis write failed")
	}
	defer func() { _ = conn.Redis.Del(context.Background(), key).Err() }()
	if got, err := conn.Redis.Get(ctx, key).Result(); err != nil || got != "module1" {
		t.Fatal("real Redis roundtrip failed")
	}
	auth := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, cfg.ToolUserID, cfg.ToolCozeUserID)
	h := httpapi.New(auth, slog.New(slog.NewJSONHandler(io.Discard, nil)), map[string]httpapi.Checker{"mysql": conn.CheckMySQL, "redis": conn.CheckRedis}, cfg.CheckTimeout)
	server := httptest.NewServer(h)
	defer server.Close()
	resp, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("real dependencies readiness = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", server.URL+"/tools/v1/integration/context", nil)
	req.Header.Set("X-Banking-Tool-Key", cfg.ToolToken)
	toolResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer toolResp.Body.Close()
	if toolResp.StatusCode != 200 {
		t.Fatalf("protected tool HTTP = %d", toolResp.StatusCode)
	}
}
