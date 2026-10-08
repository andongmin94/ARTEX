package chatgpt

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type identityClaims struct {
	jwt.RegisteredClaims
	Nonce string `json:"nonce"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type verificationKey struct {
	key any
	alg string
}
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (c *Client) verifyIdentity(ctx context.Context, raw, clientID, nonce string, requireNonce bool) (*identityClaims, error) {
	if raw == "" || len(raw) > 256<<10 {
		return nil, errInvalidIdentity
	}
	claims := &identityClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 256 {
			return nil, errInvalidIdentity
		}
		key, err := c.verificationKey(ctx, kid)
		if err != nil {
			return nil, err
		}
		if key.alg != "" && key.alg != t.Method.Alg() {
			return nil, errInvalidIdentity
		}
		return key.key, nil
	}, jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}),
		jwt.WithIssuer(c.endpoints.issuer), jwt.WithAudience(clientID), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second), jwt.WithTimeFunc(c.now))
	if err != nil || !token.Valid || claims.Subject == "" || claims.IssuedAt == nil || claims.ExpiresAt == nil || !claims.ExpiresAt.After(claims.IssuedAt.Time) {
		return nil, errInvalidIdentity
	}
	if (requireNonce || claims.Nonce != "") && (nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1) {
		return nil, errInvalidIdentity
	}
	return claims, nil
}

func (c *Client) verificationKey(ctx context.Context, kid string) (verificationKey, error) {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	if key, ok := c.keys[kid]; ok && c.now().Sub(c.keysAt) < time.Hour {
		return key, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints.jwks, nil)
	if err != nil {
		return verificationKey{}, errInvalidIdentity
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return verificationKey{}, errInvalidIdentity
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return verificationKey{}, errInvalidIdentity
	}
	var payload struct {
		Keys []jwk `json:"keys"`
	}
	if readJSON(resp.Body, &payload) != nil || len(payload.Keys) == 0 || len(payload.Keys) > 100 {
		return verificationKey{}, errInvalidIdentity
	}
	keys := make(map[string]verificationKey)
	for _, entry := range payload.Keys {
		if entry.Kid == "" || (entry.Use != "" && entry.Use != "sig") {
			continue
		}
		if _, exists := keys[entry.Kid]; exists {
			return verificationKey{}, errInvalidIdentity
		}
		key, err := parseJWK(entry)
		if err != nil {
			continue
		}
		keys[entry.Kid] = verificationKey{key, entry.Alg}
	}
	c.keys = keys
	c.keysAt = c.now()
	key, ok := keys[kid]
	if !ok {
		return verificationKey{}, errInvalidIdentity
	}
	return key, nil
}

func parseJWK(k jwk) (any, error) {
	decode := base64.RawURLEncoding.DecodeString
	switch k.Kty {
	case "RSA":
		n, err := decode(k.N)
		if err != nil || len(n) < 256 || len(n) > 1024 {
			return nil, errInvalidIdentity
		}
		e, err := decode(k.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errInvalidIdentity
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent<<8 | int(b)
		}
		if exponent < 3 || exponent%2 == 0 {
			return nil, errInvalidIdentity
		}
		if k.Alg != "" && !strings.HasPrefix(k.Alg, "RS") && !strings.HasPrefix(k.Alg, "PS") {
			return nil, errInvalidIdentity
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}, nil
	case "EC":
		var curve elliptic.Curve
		var alg string
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
			alg = "ES256"
		case "P-384":
			curve = elliptic.P384()
			alg = "ES384"
		case "P-521":
			curve = elliptic.P521()
			alg = "ES512"
		default:
			return nil, errInvalidIdentity
		}
		if k.Alg != "" && k.Alg != alg {
			return nil, errInvalidIdentity
		}
		x, err := decode(k.X)
		if err != nil {
			return nil, errInvalidIdentity
		}
		y, err := decode(k.Y)
		if err != nil {
			return nil, errInvalidIdentity
		}
		px, py := new(big.Int).SetBytes(x), new(big.Int).SetBytes(y)
		if !curve.IsOnCurve(px, py) {
			return nil, errInvalidIdentity
		}
		return &ecdsa.PublicKey{Curve: curve, X: px, Y: py}, nil
	case "OKP":
		if k.Crv != "Ed25519" || (k.Alg != "" && k.Alg != "EdDSA") {
			return nil, errInvalidIdentity
		}
		x, err := decode(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, errInvalidIdentity
		}
		return ed25519.PublicKey(x), nil
	}
	return nil, errInvalidIdentity
}
