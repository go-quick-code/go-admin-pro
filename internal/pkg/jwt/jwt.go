package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// Claims 是登录态中携带的信息。
type Claims struct {
	UserID   int64  `json:"userId"`
	TenantID int64  `json:"tenantId"`
	Username string `json:"username"`
	jwtlib.RegisteredClaims
}

type Manager struct {
	secret []byte
	expire time.Duration
	issuer string
}

func NewManager(secret string, expire time.Duration, issuer string) *Manager {
	return &Manager{secret: []byte(secret), expire: expire, issuer: issuer}
}

func (m *Manager) Expire() time.Duration { return m.expire }

// Generate 生成 access token。
func (m *Manager) Generate(userID, tenantID int64, username string) (string, time.Time, error) {
	now := time.Now()
	expireAt := now.Add(m.expire)
	claims := &Claims{
		UserID:   userID,
		TenantID: tenantID,
		Username: username,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwtlib.NewNumericDate(now),
			NotBefore: jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(expireAt),
			ID:        randomID(),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expireAt, nil
}

// Parse 校验并解析 token。
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwtlib.ParseWithClaims(tokenString, claims, func(t *jwtlib.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	}, jwtlib.WithIssuer(m.issuer), jwtlib.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
