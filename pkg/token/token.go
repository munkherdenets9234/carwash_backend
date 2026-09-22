// Package token issues and verifies the bearer tokens this service accepts.
//
// The claim set is deliberately small: who the caller is and what role they
// hold. Everything else a handler needs is looked up from the database, so a
// stale token cannot carry a stale permission — changing someone's role takes
// effect on their next request, not on their next login.
package token

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the payload of an issued token. Role is one of the values in
// models.Role; it is checked against a fixed list by middleware.Auth.Require,
// so an unknown role authorises nothing rather than everything.
type Claims struct {
	ID     string `json:"jti"`
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

type Maker struct {
	secret []byte
}

// NewMaker rejects a short secret rather than padding it. A 32-byte minimum
// is checked again in config.Validate so the failure is reported alongside
// every other configuration problem instead of one restart later.
func NewMaker(secret string) (*Maker, error) {
	if len(secret) < 32 {
		return nil, errors.New("token secret must be at least 32 characters")
	}
	return &Maker{secret: []byte(secret)}, nil
}

func (m *Maker) CreateToken(userID, role string, duration time.Duration) (string, *Claims, error) {
	claims := &Claims{
		ID:     uuid.NewString(),
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(m.secret)
	return signed, claims, err
}

func (m *Maker) VerifyToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		// Without this check a caller could present an "alg: none" token and
		// be believed.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !t.Valid {
		return nil, errors.New("invalid or expired token")
	}
	return claims, nil
}
