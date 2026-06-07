// Package auth implements v0.1 local-account authentication: bcrypt passwords,
// JWT access/refresh tokens, login lockout, and step-up re-auth. OIDC is added
// at v0.3 behind the same IdentityAdapter interface (docs/07 §7.5).
package auth

import (
	"fmt"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost per docs/07 §7.5 (cost=12).
const BcryptCost = 12

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash: %w", err)
	}
	return string(b), nil
}

// VerifyPassword reports whether pw matches the bcrypt hash.
func VerifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// CheckPolicy enforces ≥12 chars and ≥3 of {lower,upper,digit,symbol} (docs/07 §7.5).
func CheckPolicy(pw string) error {
	if len(pw) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	var lower, upper, digit, sym bool
	for _, r := range pw {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			sym = true
		}
	}
	classes := 0
	for _, ok := range []bool{lower, upper, digit, sym} {
		if ok {
			classes++
		}
	}
	if classes < 3 {
		return fmt.Errorf("password must include at least 3 of: lowercase, uppercase, digit, symbol")
	}
	return nil
}
