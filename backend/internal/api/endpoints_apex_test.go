package api

import "testing"

// TestIsPlatformDefaultDomain_SurrogateHostIsExempt pins that a hostname the
// platform issued itself needs no domain authorization. The surrogate host of
// the 2026-09-22 incident (app "aria") is the named case: gating it would break
// every upload deploy, which is the one activation path measured to work.
func TestIsPlatformDefaultDomain_SurrogateHostIsExempt(t *testing.T) {
	if !isPlatformDefaultDomain("aria-f9b214.dada-tuda.ru", "dada-tuda.ru") {
		t.Fatalf("surrogate host must be exempt from the apex gate")
	}
	if !isPlatformDefaultDomain("dada-tuda.ru", "dada-tuda.ru") {
		t.Fatalf("the base domain itself must be exempt")
	}
}

// TestIsPlatformDefaultDomain_UserDomainIsGated pins that a domain the user
// owns is NOT exempt, so it must carry a verified authorization. The named case
// is ariatala.ariaatashin.ir, registered as an endpoint before verification and
// then permanently unattachable behind 409 fqdn_taken.
func TestIsPlatformDefaultDomain_UserDomainIsGated(t *testing.T) {
	if isPlatformDefaultDomain("ariatala.ariaatashin.ir", "dada-tuda.ru") {
		t.Fatalf("a user-owned domain must not be exempt from the apex gate")
	}
}

// TestIsPlatformDefaultDomain_SuffixIsNotSubstring guards the classic
// suffix-matching hole: a domain that merely ENDS with the base string without
// a dot boundary belongs to somebody else and must stay gated.
func TestIsPlatformDefaultDomain_SuffixIsNotSubstring(t *testing.T) {
	if isPlatformDefaultDomain("evildada-tuda.ru", "dada-tuda.ru") {
		t.Fatalf("suffix without a dot boundary must not be treated as ours")
	}
	if isPlatformDefaultDomain("dada-tuda.ru.attacker.com", "dada-tuda.ru") {
		t.Fatalf("base appearing mid-name must not be treated as ours")
	}
}

// TestIsPlatformDefaultDomain_EmptyInputsAreGated pins the fail-closed
// direction: with no configured base, or no fqdn, nothing is exempt, so the
// authorization lookup still runs rather than being skipped.
func TestIsPlatformDefaultDomain_EmptyInputsAreGated(t *testing.T) {
	if isPlatformDefaultDomain("example.com", "") {
		t.Fatalf("empty base must not exempt anything")
	}
	if isPlatformDefaultDomain("", "dada-tuda.ru") {
		t.Fatalf("empty fqdn must not be exempt")
	}
}
