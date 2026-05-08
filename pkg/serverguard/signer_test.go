package serverguard

import (
	"testing"
	"time"
)

func TestSignerIssueAndVerify(t *testing.T) {
	signer := NewSigner("secret")
	token, err := signer.Issue("owner", time.Minute)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := signer.Verify(token, "owner"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestSignerRejectsWrongSubject(t *testing.T) {
	signer := NewSigner("secret")
	token, err := signer.Issue("owner", time.Minute)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := signer.Verify(token, "relay"); err == nil {
		t.Fatal("expected subject mismatch error")
	}
}
