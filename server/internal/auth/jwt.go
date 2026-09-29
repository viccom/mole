package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"moleAgent_Serv/internal/core"
)

type JWTManager struct {
	secret     []byte
	expiry     time.Duration
}

func NewJWTManager(secret string, expiry time.Duration) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
		expiry: expiry,
	}
}

func (jm *JWTManager) GenerateToken(claims *core.Claims) (string, time.Time, error) {
	exp := time.Now().Add(jm.expiry)
	tokenClaims := jwt.MapClaims{
		"user_id":  claims.UserID,
		"username": claims.Username,
		"roles":    claims.Roles,
		"exp":      exp.Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenClaims)
	signed, err := token.SignedString(jm.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

func (jm *JWTManager) VerifyToken(tokenString string) (*core.Claims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("invalid signing method")
		}
		return jm.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, core.ErrTokenExpired
		}
		return nil, core.ErrTokenInvalid
	}
	if !token.Valid {
		return nil, core.ErrTokenInvalid
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, core.ErrTokenInvalid
	}

	userID, _ := claims["user_id"].(string)
	username, _ := claims["username"].(string)
	var roles []string
	if r, ok := claims["roles"].([]interface{}); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				roles = append(roles, s)
			}
		}
	}

	return &core.Claims{
		UserID:   userID,
		Username: username,
		Roles:    roles,
	}, nil
}
