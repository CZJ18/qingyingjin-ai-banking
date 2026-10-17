package platform

import (
	"context"
	"database/sql"
	driver "github.com/go-sql-driver/mysql"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net"
	"net/url"
	"strconv"
	"time"
)

type Connections struct {
	DB    *gorm.DB
	SQL   *sql.DB
	Redis *redis.Client
}

func Open(c config.Config) (*Connections, error) {
	var dialector gorm.Dialector
	if c.DatabaseDriver == "postgres" {
		connectionURL := url.URL{Scheme: "postgres", User: url.UserPassword(c.PostgresUser, c.PostgresPassword), Host: net.JoinHostPort(c.PostgresHost, strconv.Itoa(c.PostgresPort)), Path: "/" + c.PostgresDatabase}
		query := url.Values{"sslmode": {c.PostgresSSLMode}, "connect_timeout": {strconv.Itoa(max(1, int(c.CheckTimeout.Seconds())))}, "timezone": {"UTC"}}
		connectionURL.RawQuery = query.Encode()
		dialector = postgres.New(postgres.Config{DSN: connectionURL.String()})
	} else {
		mc := driver.NewConfig()
		mc.User = c.MySQLUser
		mc.Passwd = c.MySQLPassword
		mc.Net = "tcp"
		mc.Addr = net.JoinHostPort(c.MySQLHost, strconv.Itoa(c.MySQLPort))
		mc.DBName = c.MySQLDatabase
		mc.ParseTime = true
		mc.Loc = time.UTC
		mc.Timeout = c.CheckTimeout
		mc.ReadTimeout = c.CheckTimeout
		mc.WriteTimeout = c.CheckTimeout
		mc.TLSConfig = c.MySQLTLS
		mc.Params = map[string]string{"charset": "utf8mb4"}
		dialector = mysql.New(mysql.Config{DSN: mc.FormatDSN(), SkipInitializeWithVersion: true})
	}
	db, err := gorm.Open(dialector, &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	r := redis.NewClient(&redis.Options{Addr: c.RedisAddr, Username: c.RedisUsername, Password: c.RedisPassword, DB: c.RedisDB, DialTimeout: c.CheckTimeout, ReadTimeout: c.CheckTimeout, WriteTimeout: c.CheckTimeout, MaxRetries: -1, ContextTimeoutEnabled: true})
	return &Connections{DB: db, SQL: sqlDB, Redis: r}, nil
}
func (c *Connections) CheckMySQL(ctx context.Context) error {
	return c.CheckDatabase(ctx)
}
func (c *Connections) CheckDatabase(ctx context.Context) error {
	// Query verifies authentication and SELECT access inside the configured schema.
	var one int
	return c.SQL.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}
func (c *Connections) CheckRedis(ctx context.Context) error { return c.Redis.Ping(ctx).Err() }
func (c *Connections) Close()                               { _ = c.Redis.Close(); _ = c.SQL.Close() }
