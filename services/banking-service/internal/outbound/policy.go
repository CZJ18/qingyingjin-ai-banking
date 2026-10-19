// Package outbound validates credential-bearing service origins before dispatch.
package outbound

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Policy struct {
	Environment string
	HTTPOrigins []string
}

func TestPolicy() Policy { return Policy{Environment: "test"} }
func (p Policy) Validate(raw string) error {
	if p.Environment == "" {
		p.Environment = "production"
	}
	if p.Environment != "production" && p.Environment != "development" && p.Environment != "test" {
		return errors.New("unknown service environment")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.ContainsAny(u.Host, "*\\% \t\r\n") {
		return errors.New("invalid service origin")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return errors.New("invalid service port")
		}
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme != "http" || p.Environment == "production" {
		return errors.New("credential-bearing services require HTTPS")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	// No DNS suffix, Docker hostname or user-supplied parameter implies trust.
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	for _, origin := range p.HTTPOrigins {
		if strings.TrimRight(raw, "/") == origin {
			return nil
		}
	}
	return errors.New("HTTP service origin is not explicitly approved")
}
func Parse(environment, origins string) (Policy, error) {
	if environment == "" {
		environment = "production"
	}
	p := Policy{Environment: environment}
	if environment != "production" && environment != "development" && environment != "test" {
		return p, errors.New("invalid BANKING_ENVIRONMENT")
	}
	if origins != "" {
		if environment == "production" {
			return p, errors.New("production HTTP exceptions are forbidden")
		}
		for _, raw := range strings.Split(origins, ",") {
			u, e := url.Parse(raw)
			if e != nil || u.Scheme != "http" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, "*\\% \t\r\n") {
				return p, errors.New("invalid approved HTTP origin")
			}
			if port := u.Port(); port != "" {
				n, e := strconv.Atoi(port)
				if e != nil || n < 1 || n > 65535 {
					return p, errors.New("invalid approved HTTP port")
				}
			}
			p.HTTPOrigins = append(p.HTTPOrigins, raw)
		}
	}
	return p, nil
}
