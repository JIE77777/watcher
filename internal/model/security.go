package model

type SecurityPosture struct {
	GeneratedAt string                    `json:"generated_at"`
	Scope       string                    `json:"scope"`
	Endpoints   []SecurityEndpointPosture `json:"endpoints"`
	Checklist   []SecurityChecklistItem   `json:"checklist"`
}

type SecurityEndpointPosture struct {
	EndpointID              string   `json:"endpoint_id"`
	Role                    string   `json:"role"`
	BindAddr                string   `json:"bind_addr,omitempty"`
	PublicURL               string   `json:"public_url,omitempty"`
	Scheme                  string   `json:"scheme,omitempty"`
	Host                    string   `json:"host,omitempty"`
	HostClass               string   `json:"host_class"`
	Exposure                string   `json:"exposure"`
	HTTPS                   bool     `json:"https"`
	HSTS                    bool     `json:"hsts"`
	OwnerTokenConfigured    bool     `json:"owner_token_configured"`
	DeviceAuthSupported     bool     `json:"device_auth_supported,omitempty"`
	ServiceProxyConfigured  bool     `json:"service_proxy_configured,omitempty"`
	SecureCookies           bool     `json:"secure_cookies,omitempty"`
	SessionSecretConfigured bool     `json:"session_secret_configured,omitempty"`
	AllowedHosts            []string `json:"allowed_hosts"`
	TrustedProxies          []string `json:"trusted_proxies"`
	RateLimitPerMinute      int      `json:"rate_limit_per_minute,omitempty"`
	MaxBodyBytes            int64    `json:"max_body_bytes,omitempty"`
}

type SecurityChecklistItem struct {
	ItemID   string `json:"item_id"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}
