package engine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ── User model ────────────────────────────────────────────────────────────────

// User represents a netwatch user account stored in the users table.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	Role         string `json:"role"`          // admin | operator | viewer
	DisplayName  string `json:"display_name,omitempty"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by,omitempty"`
	LastLoginAt  string `json:"last_login_at,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
}

// UserPublic is the User without the password hash, suitable for API responses.
type UserPublic struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name,omitempty"`
	CreatedAt   string `json:"created_at"`
	CreatedBy   string `json:"created_by,omitempty"`
	LastLoginAt string `json:"last_login_at,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// Public returns a copy of the user without the password hash.
func (u User) Public() UserPublic {
	return UserPublic{
		ID:          u.ID,
		Username:    u.Username,
		Role:        u.Role,
		DisplayName: u.DisplayName,
		CreatedAt:   u.CreatedAt,
		CreatedBy:   u.CreatedBy,
		LastLoginAt: u.LastLoginAt,
		Disabled:    u.Disabled,
	}
}

// ── Password hashing ──────────────────────────────────────────────────────────

const bcryptCost = 12

// HashPassword creates a bcrypt hash of the given plaintext password.
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword verifies a plaintext password against a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ── JWT (minimal, no third-party dependency) ──────────────────────────────────

const jwtTTL = 24 * time.Hour

// JWTHeader is the fixed header for our HS256 JWTs.
var jwtHeaderB64 = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// JWTClaims represents the payload of a netwatch JWT.
type JWTClaims struct {
	Sub      string `json:"sub"`      // user ID
	Username string `json:"username"`
	Role     string `json:"role"`
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
}

// SignJWT creates a new HS256 JWT token signed with the given secret.
func SignJWT(claims JWTClaims, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("jwt: signing secret is empty")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("jwt: marshal claims: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := jwtHeaderB64 + "." + payloadB64

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return unsigned + "." + sig, nil
}

// VerifyJWT parses and verifies an HS256 JWT token. Returns the claims if valid.
func VerifyJWT(tokenStr, secret string) (*JWTClaims, error) {
	if secret == "" {
		return nil, errors.New("jwt: signing secret is empty")
	}
	parts := strings.SplitN(tokenStr, ".", 4)
	if len(parts) != 3 {
		return nil, errors.New("jwt: malformed token")
	}

	// Verify signature
	unsigned := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, errors.New("jwt: invalid signature")
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("jwt: decode payload: %w", err)
	}
	var claims JWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("jwt: unmarshal claims: %w", err)
	}

	// Check expiry
	if time.Now().Unix() > claims.Exp {
		return nil, errors.New("jwt: token expired")
	}

	return &claims, nil
}

// NewJWTForUser creates a fresh JWT for the given user, signed with the setup token.
func NewJWTForUser(user User, setupToken string) (string, error) {
	now := time.Now()
	claims := JWTClaims{
		Sub:      user.ID,
		Username: user.Username,
		Role:     user.Role,
		Iat:      now.Unix(),
		Exp:      now.Add(jwtTTL).Unix(),
	}
	return SignJWT(claims, setupToken)
}

// ── Frontend settings model ───────────────────────────────────────────────────

// FrontendSettings holds cluster-wide frontend configuration stored in the
// frontend_settings table. The ID is always "cluster_nodes".
type FrontendSettings struct {
	ID   string   `json:"id"`
	URLs []string `json:"urls"`
}
