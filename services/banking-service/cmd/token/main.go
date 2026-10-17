package main

import (
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/identity"
	"os"
	"time"
)

func main() {
	envFile := flag.String("env-file", "", "optional env file")
	user := flag.String("user-id", "", "trusted Banking user ID")
	cozeUser := flag.String("coze-user-id", "", "operator verified Coze user ID")
	ttl := flag.Duration("ttl", time.Hour, "lifetime, maximum 24h")
	flag.Parse()
	if *envFile != "" {
		if err := godotenv.Load(*envFile); err != nil {
			fmt.Fprintln(os.Stderr, "cannot load env file")
			os.Exit(1)
		}
	}
	issuer := os.Getenv("BANKING_JWT_ISSUER")
	if issuer == "" {
		issuer = "qingyingjin"
	}
	aud := os.Getenv("BANKING_JWT_AUDIENCE")
	if aud == "" {
		aud = "banking-service"
	}
	auth := identity.New(os.Getenv("BANKING_USER_JWT_SECRET"), issuer, aud, "", "", "")
	token, err := auth.IssueUser(*user, *cozeUser, *ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(token)
}
