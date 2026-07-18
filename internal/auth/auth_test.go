package auth

import "testing"

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("admin123456")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "admin123456" {
		t.Fatal("password stored in plaintext")
	}
	if !CheckPasswordHash("admin123456", hash) {
		t.Fatal("correct password should verify")
	}
	if CheckPasswordHash("wrong-password", hash) {
		t.Fatal("wrong password must not verify")
	}
}

func TestHashPasswordUnique(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("bcrypt hashes should differ due to random salt")
	}
}

func TestGenerateSessionID(t *testing.T) {
	id := GenerateSessionID()
	if len(id) != 64 {
		t.Fatalf("session id should be 64 hex chars, got %d", len(id))
	}
	if GenerateSessionID() == GenerateSessionID() {
		t.Fatal("session ids should be unique")
	}
}

func TestGenerateCSRFToken(t *testing.T) {
	if len(GenerateCSRFToken()) != 64 {
		t.Fatal("csrf token should be 64 hex chars")
	}
}
