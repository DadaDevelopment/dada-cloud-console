package tbank

import (
	"errors"
	"strconv"
)

// ErrINNInvalid is returned by ValidateINN for any string that is not a
// syntactically and checksum-valid Russian INN (10 digits for a legal
// entity, 12 for a sole proprietor / individual).
var ErrINNInvalid = errors.New("tbank: invalid INN")

var innWeights10 = []int{2, 4, 10, 3, 5, 9, 4, 6, 8}
var innWeights12N1 = []int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
var innWeights12N2 = []int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}

// ValidateINN checks a Russian taxpayer identification number against the
// FNS control-digit algorithm. It accepts the two lengths FNS issues: 10
// digits for a legal entity, 12 for a sole proprietor or individual. Any
// other length, or a non-digit character, is rejected before the checksum
// is even computed.
func ValidateINN(inn string) error {
	digits, err := innDigits(inn)
	if err != nil {
		return err
	}
	switch len(digits) {
	case 10:
		check := innControlDigit(digits[:9], innWeights10)
		if check != digits[9] {
			return ErrINNInvalid
		}
		return nil
	case 12:
		n1 := innControlDigit(digits[:10], innWeights12N1)
		n2 := innControlDigit(digits[:11], innWeights12N2)
		if n1 != digits[10] || n2 != digits[11] {
			return ErrINNInvalid
		}
		return nil
	default:
		return ErrINNInvalid
	}
}

func innDigits(inn string) ([]int, error) {
	if len(inn) != 10 && len(inn) != 12 {
		return nil, ErrINNInvalid
	}
	digits := make([]int, len(inn))
	for i, r := range inn {
		if r < '0' || r > '9' {
			return nil, ErrINNInvalid
		}
		d, err := strconv.Atoi(string(r))
		if err != nil {
			return nil, ErrINNInvalid
		}
		digits[i] = d
	}
	return digits, nil
}

func innControlDigit(digits []int, weights []int) int {
	sum := 0
	for i, w := range weights {
		sum += digits[i] * w
	}
	return (sum % 11) % 10
}
