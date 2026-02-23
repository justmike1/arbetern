package main

import (
	"log"
	"net"
	"net/http"
	"strings"
)

// ipWhitelist returns middleware that restricts access to the given CIDR list.
// If allowedCIDRs is empty, all requests are allowed (whitelist disabled).
// The middleware checks X-Forwarded-For first (for requests behind a load balancer),
// then falls back to the direct remote address.
func ipWhitelist(allowedCIDRs string, next http.Handler) http.Handler {
	cidrs := parseCIDRs(allowedCIDRs)
	if len(cidrs) == 0 {
		return next // No restriction configured.
	}

	log.Printf("UI IP whitelist enabled: %s", allowedCIDRs)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := clientIP(r)
		ip := net.ParseIP(clientIP)
		if ip == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		for _, cidr := range cidrs {
			if cidr.Contains(ip) {
				next.ServeHTTP(w, r)
				return
			}
		}

		log.Printf("UI access denied for IP %s", clientIP)
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

func parseCIDRs(raw string) []*net.IPNet {
	if raw == "" {
		return nil
	}
	var nets []*net.IPNet
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Allow bare IPs like "1.2.3.4" by appending /32 or /128.
		if !strings.Contains(s, "/") {
			if strings.Contains(s, ":") {
				s += "/128"
			} else {
				s += "/32"
			}
		}
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			log.Printf("WARNING: ignoring invalid CIDR %q: %v", s, err)
			continue
		}
		nets = append(nets, cidr)
	}
	return nets
}

func clientIP(r *http.Request) string {
	// X-Forwarded-For can contain multiple IPs; the first is the original client.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// Fall back to direct connection IP.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
