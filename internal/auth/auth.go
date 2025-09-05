package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return "", fmt.Errorf("HashPassword: %w", err)
	}

	return string(hash), nil
}

func CheckPasswordHash(password, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func MakeJWT(
	userID uuid.UUID,
	tokenSecret string,
	expiresIn time.Duration,
) (string, error) {
	tok := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		jwt.RegisteredClaims{
			Issuer:    "chirpy",
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(expiresIn)),
			Subject:   userID.String(),
		},
	)

	tokenString, err := tok.SignedString([]byte(tokenSecret))
	if err != nil {
		return "", fmt.Errorf("MakeJWT: %w", err)
	}

	return tokenString, nil
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claims := jwt.RegisteredClaims{}
	tok, err := jwt.ParseWithClaims(
		tokenString,
		&claims,
		func(token *jwt.Token) (any, error) {
			return []byte(tokenSecret), nil
		},
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ValidateJWT: %w", err)
	}

	uuidString, err := tok.Claims.GetSubject()
	if err != nil {
		return uuid.Nil, fmt.Errorf("ValidateJWT: %w", err)
	}

	issuer, err := tok.Claims.GetIssuer()
	if err != nil {
		return uuid.Nil, fmt.Errorf("ValidateJWT: %w", err)
	}

	if issuer != "chirpy" {
		return uuid.Nil, fmt.Errorf("invalid issuer")
	}

	tokenUUID, err := uuid.Parse(uuidString)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ValidateJWT: %w", err)
	}

	return tokenUUID, nil
}

func GetBearerToken(headers http.Header) (string, error) {
	tokenString := headers.Get("Authorization")
	if tokenString == "" {
		return "", fmt.Errorf("No token string provided")
	}

	tokenString = strings.TrimSpace(strings.TrimPrefix(tokenString, "Bearer "))

	return tokenString, nil
}

func MakeRefreshToken() (string, error) {
	byteToken := make([]byte, 32)

	rand.Read(byteToken)

	refreshToken := hex.EncodeToString(byteToken)

	return refreshToken, nil
}
