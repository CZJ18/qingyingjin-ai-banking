package main

import (
	"context"
	"flag"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
	"log"
	"os"
	"time"
)

func main() {
	env := flag.String("env-file", "", "configuration file")
	expected := flag.String("expected-database", "", "refuse migration unless the resolved database exactly matches this name")
	testOnly := flag.Bool("test-only", false, "additionally require an explicit isolated *_test database")
	flag.Parse()
	if *env != "" {
		if err := godotenv.Load(*env); err != nil {
			log.Fatal("cannot load env file")
		}
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if err = validateMigrationTarget(cfg, *expected, *testOnly); err != nil {
		log.Fatal(err)
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		log.Fatal("cannot open database")
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = migration.Apply(ctx, conn.SQL); err != nil {
		log.Fatal(err)
	}
	log.Printf("Banking %s migrations through %03d applied/verified", cfg.DatabaseDriver, migration.CurrentVersion)
}
