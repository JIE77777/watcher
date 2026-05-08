package serverguard

import (
	"net"
	"net/http"
	"strings"
)

type IPResolver struct {
	trusted []*net.IPNet
}

func NewIPResolver(trustedCIDRs []string) (IPResolver, error) {
	resolver := IPResolver{}
	for _, cidr := range trustedCIDRs {
		trimmed := strings.TrimSpace(cidr)
		if trimmed == "" {
			continue
		}
		_, network, err := net.ParseCIDR(trimmed)
		if err != nil {
			return IPResolver{}, err
		}
		resolver.trusted = append(resolver.trusted, network)
	}
	return resolver, nil
}

func (r IPResolver) ClientIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	host = strings.TrimSpace(host)
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if len(r.trusted) == 0 || !r.isTrusted(ip) {
		return ip.String()
	}

	xff := req.Header.Get("X-Forwarded-For")
	for _, part := range strings.Split(xff, ",") {
		candidate := net.ParseIP(strings.TrimSpace(part))
		if candidate != nil {
			return candidate.String()
		}
	}
	if realIP := net.ParseIP(strings.TrimSpace(req.Header.Get("X-Real-IP"))); realIP != nil {
		return realIP.String()
	}
	return ip.String()
}

func (r IPResolver) isTrusted(ip net.IP) bool {
	for _, network := range r.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
