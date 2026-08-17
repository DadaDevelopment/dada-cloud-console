package tbank

import (
	"errors"
	"testing"
)

func TestValidateINN_ValidLegalEntity10Digits(t *testing.T) {
	if err := ValidateINN("7707083893"); err != nil {
		t.Fatalf("ValidateINN(7707083893) = %v, want nil (real 10-digit legal-entity INN)", err)
	}
}

func TestValidateINN_InvalidLegalEntityChecksum(t *testing.T) {
	if err := ValidateINN("7707083894"); !errors.Is(err, ErrINNInvalid) {
		t.Fatalf("ValidateINN(7707083894) = %v, want ErrINNInvalid (checksum digit does not match)", err)
	}
}

func TestValidateINN_ValidIndividualEntrepreneur12Digits(t *testing.T) {
	if err := ValidateINN("500100732259"); err != nil {
		t.Fatalf("ValidateINN(500100732259) = %v, want nil (real 12-digit IP/individual INN)", err)
	}
}

func TestValidateINN_InvalidIndividualEntrepreneurChecksum(t *testing.T) {
	if err := ValidateINN("500100732258"); !errors.Is(err, ErrINNInvalid) {
		t.Fatalf("ValidateINN(500100732258) = %v, want ErrINNInvalid (second checksum digit does not match)", err)
	}
}

func TestValidateINN_WrongLength(t *testing.T) {
	for _, inn := range []string{"", "123", "12345678", "1234567890123"} {
		if err := ValidateINN(inn); !errors.Is(err, ErrINNInvalid) {
			t.Fatalf("ValidateINN(%q) = %v, want ErrINNInvalid (neither 10 nor 12 digits)", inn, err)
		}
	}
}

func TestValidateINN_NonDigitCharacters(t *testing.T) {
	if err := ValidateINN("770708389X"); !errors.Is(err, ErrINNInvalid) {
		t.Fatalf("ValidateINN(770708389X) = %v, want ErrINNInvalid (non-digit character)", err)
	}
}
