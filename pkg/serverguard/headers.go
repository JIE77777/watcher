package serverguard

import "net/http"

type HeadersConfig struct {
	EnableHSTS            bool
	HSTSMaxAgeSeconds     int
	ContentSecurityPolicy string
}

func SecurityHeaders(cfg HeadersConfig) Middleware {
	csp := cfg.ContentSecurityPolicy
	if csp == "" {
		csp = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'"
	}
	hstsMaxAge := cfg.HSTSMaxAgeSeconds
	if hstsMaxAge <= 0 {
		hstsMaxAge = 31536000
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headers := w.Header()
			headers.Set("X-Content-Type-Options", "nosniff")
			headers.Set("X-Frame-Options", "DENY")
			headers.Set("Referrer-Policy", "no-referrer")
			headers.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
			headers.Set("Content-Security-Policy", csp)
			if cfg.EnableHSTS {
				headers.Set("Strict-Transport-Security", "max-age="+itoa(hstsMaxAge)+"; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	n := v
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
