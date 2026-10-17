// sms-inbox is an explicitly invoked local development facility, never a tool
// exposed to an Agent, public HTTP route, frontend bundle or normal server log.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/smsprovider"
	"github.com/redis/go-redis/v9"
)

type envFiles []string

func (e *envFiles) String() string     { return strings.Join(*e, ",") }
func (e *envFiles) Set(s string) error { *e = append(*e, s); return nil }
func main() {
	var paths envFiles
	flag.Var(&paths, "env-file", "private configuration; repeat for base runtime and SMS override")
	phone := flag.String("phone", "", "recipient mainland number")
	purpose := flag.String("purpose", "", "register, verify_phone or password_reset")
	allowed := flag.Bool("allow-development-code", false, "explicitly allow printing the local development OTP to this terminal")
	flag.Parse()
	if !*allowed || len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "Local inbox requires private configuration and -allow-development-code; never use in production")
		os.Exit(1)
	}
	values := map[string]string{}
	for _, p := range paths {
		m, err := godotenv.Read(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot read private configuration")
			os.Exit(1)
		}
		for k, v := range m {
			values[k] = v
		}
	}
	cfg, err := config.Load(func(k string) string { return values[k] })
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid development configuration")
		os.Exit(1)
	}
	if cfg.SMS.Provider != "development" {
		fmt.Fprintln(os.Stderr, "Local inbox requires explicit development mode")
		os.Exit(1)
	}
	cache := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Username: cfg.RedisUsername, Password: cfg.RedisPassword, DB: cfg.RedisDB, MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	defer cache.Close()
	sender, err := smsprovider.NewDevelopment(cache, cfg.SMS.OTPSecret)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot open development inbox")
		os.Exit(1)
	}
	number := strings.TrimSpace(*phone)
	if !strings.HasPrefix(number, "+86") {
		number = "+86" + number
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	message, err := sender.Read(ctx, number, *purpose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No available development message for this recipient/purpose; it may be expired")
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(message) != nil {
		os.Exit(1)
	}
}
