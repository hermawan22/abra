package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/config"
)

type authAction string

const (
	authActionRead  authAction = "read"
	authActionWrite authAction = "write"
	authActionOps   authAction = "ops"
)

type authContextKey struct{}

type apiPrincipal struct {
	token     string
	roles     map[string]struct{}
	scopes    []string
	allScopes bool
}

func (h *handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := authenticate(r, h.cfg.APIKeys, h.cfg.AllowUnauthenticatedDev)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, principal)))
	}
}

func (h *handler) requireAccess(w http.ResponseWriter, r *http.Request, action authAction, scope string) bool {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	scope = strings.TrimSpace(scope)
	if principal.allows(action, scope) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":          "forbidden",
		"required_role":  string(action),
		"required_scope": scope,
	})
	return false
}

func authGate(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authenticate(r *http.Request, keys []string, allowUnauthenticatedDev bool) (*apiPrincipal, bool) {
	if len(keys) == 0 {
		if allowUnauthenticatedDev {
			return anonymousAdmin(), true
		}
		return nil, false
	}
	token := requestToken(r)
	if token == "" {
		return nil, false
	}
	for _, principal := range parseAPIKeys(keys) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(principal.token)) == 1 {
			return principal, true
		}
	}
	return nil, false
}

func requestToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return strings.TrimSpace(r.Header.Get("x-api-key"))
}

func principalFromContext(ctx context.Context) *apiPrincipal {
	principal, _ := ctx.Value(authContextKey{}).(*apiPrincipal)
	return principal
}

func anonymousAdmin() *apiPrincipal {
	return &apiPrincipal{
		roles:     map[string]struct{}{"admin": {}},
		allScopes: true,
	}
}

func parseAPIKeys(specs []string) []*apiPrincipal {
	principals := make([]*apiPrincipal, 0, len(specs))
	for _, spec := range specs {
		principal, ok := parseAPIKeySpec(spec)
		if ok {
			principals = append(principals, principal)
		}
	}
	return principals
}

func parseAPIKeySpec(spec string) (*apiPrincipal, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, false
	}
	token, rawOptions, hasOptions := strings.Cut(spec, "|")
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, false
	}
	principal := &apiPrincipal{
		token:  token,
		roles:  map[string]struct{}{},
		scopes: []string{},
	}
	if !hasOptions {
		principal.roles["admin"] = struct{}{}
		principal.allScopes = true
		return principal, true
	}
	for _, option := range strings.FieldsFunc(rawOptions, func(r rune) bool { return r == ';' || r == '|' }) {
		key, value, ok := strings.Cut(option, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		values := splitAuthValues(value)
		switch key {
		case "role", "roles":
			for _, role := range values {
				if isKnownRole(role) {
					principal.roles[role] = struct{}{}
				}
			}
		case "scope", "scopes":
			for _, scope := range values {
				if scope == "*" {
					principal.allScopes = true
					continue
				}
				principal.scopes = append(principal.scopes, scope)
			}
		}
	}
	if len(principal.roles) == 0 {
		principal.roles["admin"] = struct{}{}
	}
	if !principal.allScopes && len(principal.scopes) == 0 {
		principal.allScopes = true
	}
	return principal, true
}

func splitAuthValues(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '+' || r == ','
	})
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.ToLower(strings.TrimSpace(part)); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func isKnownRole(role string) bool {
	switch role {
	case "admin", "writer", "reader", "ops":
		return true
	default:
		return false
	}
}

func (p *apiPrincipal) allows(action authAction, scope string) bool {
	if p == nil || !p.allowsAction(action) {
		return false
	}
	if scope == "" {
		return p.allScopes
	}
	return p.allowsScope(scope)
}

func (p *apiPrincipal) allowsAction(action authAction) bool {
	if p.hasRole("admin") {
		return true
	}
	switch action {
	case authActionRead:
		return p.hasRole("reader") || p.hasRole("writer")
	case authActionWrite:
		return p.hasRole("writer")
	case authActionOps:
		return p.hasRole("ops")
	default:
		return false
	}
}

func (p *apiPrincipal) hasRole(role string) bool {
	_, ok := p.roles[role]
	return ok
}

func (p *apiPrincipal) allowsScope(scope string) bool {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return p.allScopes
	}
	if p.allScopes {
		return true
	}
	for _, allowed := range p.scopes {
		if scopeMatches(allowed, scope) {
			return true
		}
	}
	return false
}

func scopeMatches(allowed, scope string) bool {
	allowed = strings.ToLower(strings.TrimSpace(allowed))
	if allowed == "*" || allowed == scope {
		return true
	}
	if strings.HasSuffix(allowed, "*") {
		return strings.HasPrefix(scope, strings.TrimSuffix(allowed, "*"))
	}
	return false
}
