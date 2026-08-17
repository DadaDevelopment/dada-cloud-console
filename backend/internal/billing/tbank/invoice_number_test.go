package tbank

import (
	"testing"
	"time"
)

func TestFormatInvoiceNumber_Shape(t *testing.T) {
	got := FormatInvoiceNumber(2026, 7)
	want := "INV-2026-00007"
	if got != want {
		t.Fatalf("FormatInvoiceNumber(2026, 7) = %q, want %q", got, want)
	}
}

func TestFormatInvoiceNumber_PadsToFiveDigits(t *testing.T) {
	got := FormatInvoiceNumber(2026, 123456)
	want := "INV-2026-123456"
	if got != want {
		t.Fatalf("FormatInvoiceNumber(2026, 123456) = %q, want %q (no truncation past 5 digits)", got, want)
	}
}

func TestFormatInvoiceNumber_DistinctSeqGivesDistinctNumbers(t *testing.T) {
	year := time.Now().UTC().Year()
	seen := make(map[string]bool)
	for seq := int64(1); seq <= 50; seq++ {
		n := FormatInvoiceNumber(year, seq)
		if seen[n] {
			t.Fatalf("FormatInvoiceNumber produced a duplicate for seq=%d: %q", seq, n)
		}
		seen[n] = true
	}
}

func TestFormatInvoiceNumber_MatchesExpectedRegex(t *testing.T) {
	got := FormatInvoiceNumber(2026, 42)
	if !invoiceNumberPattern.MatchString(got) {
		t.Fatalf("FormatInvoiceNumber output %q does not match the INV-\\d{4}-\\d{5} shape the reconciler regex expects", got)
	}
}
