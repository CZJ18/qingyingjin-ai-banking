package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ModelDailyCalls, ModelDailyTokens, ModelReservationTokens                                         int64
	SMS                                                                                               SMSConfig
	AuthCookieSecure                                                                                  bool
	DatabaseDriver                                                                                    string
	PostgresHost, PostgresUser, PostgresPassword, PostgresDatabase, PostgresSSLMode                   string
	PostgresPort                                                                                      int
	LangGraphServiceToken                                                                             string
	LifestyleGoalWorkflowID, LifestyleAgentWorkflowID                                                 string
	HTTPAddr, LogLevel                                                                                string
	CheckTimeout                                                                                      time.Duration
	MySQLHost                                                                                         string
	MySQLPort                                                                                         int
	MySQLUser, MySQLPassword, MySQLDatabase                                                           string
	MySQLTLS                                                                                          string
	RedisAddr, RedisUsername, RedisPassword                                                           string
	RedisDB                                                                                           int
	JWTSecret, JWTIssuer, JWTAudience                                                                 string
	ToolToken, ToolUserID, ToolCozeUserID                                                             string
	CozeBaseURL, CozePAT                                                                              string
	CozeTimeout                                                                                       time.Duration
	GoalWorkflowID                                                                                    string
	BillingWorkflowID                                                                                 string
	OrchestratorBaseURL                                                                               string
	SupervisorWorkflowID, BillingAgentWorkflowID, SubscriptionAgentWorkflowID, SavingsAgentWorkflowID string
}

func Load(get func(string) string) (Config, error) {
	var c Config
	if err := loadModelBudget(&c, get); err != nil {
		return c, err
	}
	value := func(k, d string) string {
		if v := get(k); v != "" {
			return v
		}
		return d
	}
	c.HTTPAddr = value("BANKING_HTTP_ADDR", "127.0.0.1:8090")
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return c, fmt.Errorf("invalid BANKING_HTTP_ADDR")
	}
	c.LogLevel = value("BANKING_LOG_LEVEL", "info")
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return c, fmt.Errorf("invalid BANKING_LOG_LEVEL")
	}
	secureCookie := value("BANKING_AUTH_COOKIE_SECURE", "true")
	if secureCookie != "true" && secureCookie != "false" {
		return c, fmt.Errorf("BANKING_AUTH_COOKIE_SECURE must be true or false")
	}
	c.AuthCookieSecure = secureCookie == "true"
	duration := func(k, d string, maximum time.Duration) (time.Duration, error) {
		v, e := time.ParseDuration(value(k, d))
		if e != nil || v <= 0 || v > maximum {
			return 0, fmt.Errorf("invalid %s", k)
		}
		return v, nil
	}
	var err error
	c.DatabaseDriver = value("BANKING_DATABASE_DRIVER", "mysql")
	if c.DatabaseDriver != "mysql" && c.DatabaseDriver != "postgres" {
		return c, fmt.Errorf("BANKING_DATABASE_DRIVER must be mysql or postgres")
	}
	if c.DatabaseDriver == "postgres" {
		if err = loadPostgres(&c, get, value); err != nil {
			return c, err
		}
	}
	c.CheckTimeout, err = duration("BANKING_CHECK_TIMEOUT", "2s", time.Minute)
	if err != nil {
		return c, err
	}
	c.CozeTimeout, err = duration("BANKING_COZE_TIMEOUT", "30s", 3*time.Minute)
	if err != nil {
		return c, err
	}
	c.MySQLHost = value("BANKING_MYSQL_HOST", "127.0.0.1")
	c.MySQLPort, err = strconv.Atoi(value("BANKING_MYSQL_PORT", "3306"))
	if err != nil || c.MySQLPort < 1 || c.MySQLPort > 65535 {
		return c, fmt.Errorf("invalid BANKING_MYSQL_PORT")
	}
	c.MySQLUser = value("BANKING_MYSQL_USER", "banking")
	c.MySQLPassword = get("BANKING_MYSQL_PASSWORD")
	c.MySQLDatabase = value("BANKING_MYSQL_DATABASE", "qingyingjin")
	for _, r := range c.MySQLDatabase {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return c, fmt.Errorf("invalid BANKING_MYSQL_DATABASE")
		}
	}
	c.MySQLTLS = value("BANKING_MYSQL_TLS", "false")
	if c.MySQLTLS != "false" && c.MySQLTLS != "true" {
		return c, fmt.Errorf("BANKING_MYSQL_TLS must be false or true")
	}
	c.RedisAddr = value("BANKING_REDIS_ADDR", "127.0.0.1:6379")
	if _, _, err := net.SplitHostPort(c.RedisAddr); err != nil {
		return c, fmt.Errorf("invalid BANKING_REDIS_ADDR")
	}
	c.RedisUsername = get("BANKING_REDIS_USERNAME")
	c.RedisPassword = get("BANKING_REDIS_PASSWORD")
	c.RedisDB, err = strconv.Atoi(value("BANKING_REDIS_DB", "0"))
	if err != nil || c.RedisDB < 0 || c.RedisDB > 15 {
		return c, fmt.Errorf("invalid BANKING_REDIS_DB")
	}
	c.JWTSecret = get("BANKING_USER_JWT_SECRET")
	c.JWTIssuer = value("BANKING_JWT_ISSUER", "qingyingjin")
	c.JWTAudience = value("BANKING_JWT_AUDIENCE", "banking-service")
	c.ToolToken = get("BANKING_TOOL_TOKEN")
	c.ToolUserID = get("BANKING_TOOL_USER_ID")
	c.ToolCozeUserID = get("BANKING_TOOL_COZE_USER_ID")
	required := map[string]string{"BANKING_REDIS_PASSWORD": c.RedisPassword}
	if c.DatabaseDriver == "mysql" {
		required["BANKING_MYSQL_PASSWORD"] = c.MySQLPassword
	}
	for k, v := range required {
		if strings.TrimSpace(v) == "" {
			return c, fmt.Errorf("%s is required", k)
		}
	}
	for k, v := range map[string]string{"BANKING_USER_JWT_SECRET": c.JWTSecret, "BANKING_TOOL_TOKEN": c.ToolToken} {
		if len(v) < 32 {
			return c, fmt.Errorf("%s requires at least 32 bytes", k)
		}
	}
	if c.JWTSecret == c.ToolToken {
		return c, fmt.Errorf("user and tool secrets must differ")
	}
	if !PositiveID(c.ToolUserID) {
		return c, fmt.Errorf("invalid BANKING_TOOL_USER_ID")
	}
	if c.ToolCozeUserID != "" && !PositiveID(c.ToolCozeUserID) {
		return c, fmt.Errorf("invalid BANKING_TOOL_COZE_USER_ID")
	}
	c.CozeBaseURL = get("BANKING_COZE_BASE_URL")
	c.LangGraphServiceToken = get("BANKING_LANGGRAPH_SERVICE_TOKEN")
	if err := loadSMS(&c, get); err != nil {
		return c, err
	}
	if c.SMS.Provider == "development" && c.AuthCookieSecure {
		return c, fmt.Errorf("development SMS requires explicit local HTTP cookie configuration")
	}
	if c.LangGraphServiceToken != "" && (len(c.LangGraphServiceToken) < 32 || strings.Contains(c.LangGraphServiceToken, "CHANGE_ME") || c.LangGraphServiceToken == c.JWTSecret || c.LangGraphServiceToken == c.ToolToken) {
		return c, fmt.Errorf("BANKING_LANGGRAPH_SERVICE_TOKEN requires a separate non-template secret of at least 32 bytes")
	}
	c.CozePAT = get("BANKING_COZE_PAT")
	c.GoalWorkflowID = get("BANKING_GOAL_WORKFLOW_ID")
	c.BillingWorkflowID = get("BANKING_BILLING_WORKFLOW_ID")
	c.OrchestratorBaseURL = value("BANKING_ORCHESTRATOR_API_BASE_URL", "http://127.0.0.1:8090")
	u, parseError := url.Parse(c.OrchestratorBaseURL)
	if parseError != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return c, fmt.Errorf("invalid BANKING_ORCHESTRATOR_API_BASE_URL")
	}
	for key, target := range map[string]*string{"COZE_LIFESTYLE_GOAL_WORKFLOW_ID": &c.LifestyleGoalWorkflowID, "BANKING_LIFESTYLE_AGENT_WORKFLOW_ID": &c.LifestyleAgentWorkflowID, "BANKING_SUPERVISOR_WORKFLOW_ID": &c.SupervisorWorkflowID, "BANKING_BILLING_AGENT_WORKFLOW_ID": &c.BillingAgentWorkflowID, "BANKING_SUBSCRIPTION_AGENT_WORKFLOW_ID": &c.SubscriptionAgentWorkflowID, "BANKING_SAVINGS_AGENT_WORKFLOW_ID": &c.SavingsAgentWorkflowID} {
		*target = get(key)
		if *target != "" && (!PositiveID(*target) || c.CozeBaseURL == "" || c.CozePAT == "") {
			return c, fmt.Errorf("%s requires a canonical positive ID and configured Coze URL/PAT", key)
		}
	}
	if c.BillingWorkflowID != "" && (!PositiveID(c.BillingWorkflowID) || c.CozeBaseURL == "" || c.CozePAT == "") {
		return c, fmt.Errorf("BANKING_BILLING_WORKFLOW_ID requires a positive ID and configured Coze URL/PAT")
	}
	if c.GoalWorkflowID != "" && (!PositiveID(c.GoalWorkflowID) || c.CozeBaseURL == "" || c.CozePAT == "") {
		return c, fmt.Errorf("BANKING_GOAL_WORKFLOW_ID requires a canonical positive ID and configured Coze URL/PAT")
	}
	for key, value := range map[string]string{"BANKING_MYSQL_PASSWORD": c.MySQLPassword, "BANKING_REDIS_PASSWORD": c.RedisPassword, "BANKING_USER_JWT_SECRET": c.JWTSecret, "BANKING_TOOL_TOKEN": c.ToolToken, "BANKING_COZE_PAT": c.CozePAT} {
		if strings.Contains(value, "CHANGE_ME") {
			return c, fmt.Errorf("%s contains a template placeholder", key)
		}
	}
	if (c.CozeBaseURL == "") != (c.CozePAT == "") {
		return c, fmt.Errorf("BANKING_COZE_BASE_URL and BANKING_COZE_PAT must be set together")
	}
	if c.CozeBaseURL != "" {
		u, e := url.Parse(c.CozeBaseURL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return c, fmt.Errorf("invalid BANKING_COZE_BASE_URL")
		}
	}
	return c, nil
}
func PositiveID(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == s
}
