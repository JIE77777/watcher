package serverguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Signer struct {
	secret []byte
}

type signedPayload struct {
	Subject string `json:"sub"`
	Expires int64  `json:"exp"`
}

func NewSigner(secret string) *Signer {
	return &Signer{secret: []byte(secret)}
}

func (s *Signer) Issue(subject string, ttl time.Duration) (string, error) {
	if len(s.secret) == 0 {
		return "", errors.New("signer secret is empty")
	}
	payload := signedPayload{
		Subject: subject,
		Expires: time.Now().UTC().Add(ttl).Unix(),
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(rawPayload)
	signature := s.signature(encodedPayload)
	return encodedPayload + "." + signature, nil
}

func (s *Signer) Verify(token, expectedSubject string) error {
	if len(s.secret) == 0 {
		return errors.New("signer secret is empty")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errors.New("invalid token format")
	}
	expectedSig := s.signature(parts[0])
	if !hmac.Equal([]byte(expectedSig), []byte(parts[1])) {
		return errors.New("invalid token signature")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var payload signedPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return err
	}
	if payload.Subject != expectedSubject {
		return errors.New("unexpected token subject")
	}
	if time.Now().UTC().Unix() > payload.Expires {
		return errors.New("token expired")
	}
	return nil
}

func (s *Signer) signature(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
