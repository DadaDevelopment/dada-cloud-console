package registry

import "testing"

func TestImageRefs(t *testing.T) {
	h := NewHarbor("https://harbor.dada-tuda.ru", "admin", "secret")

	if got := h.Host(); got != "harbor.dada-tuda.ru" {
		t.Errorf("Host() = %q", got)
	}
	if got := h.ImageURI("acme", "web", "sha256:deadbeef"); got != "harbor.dada-tuda.ru/acme/web@sha256:deadbeef" {
		t.Errorf("ImageURI = %q", got)
	}
	// digest without prefix gets normalized.
	if got := h.ImageURI("acme", "web", "deadbeef"); got != "harbor.dada-tuda.ru/acme/web@sha256:deadbeef" {
		t.Errorf("ImageURI(no prefix) = %q", got)
	}
	if got := h.ImageTag("acme", "web", "v1"); got != "harbor.dada-tuda.ru/acme/web:v1" {
		t.Errorf("ImageTag = %q", got)
	}
	if got := h.CacheRef("acme", "web"); got != "harbor.dada-tuda.ru/acme/web:buildcache" {
		t.Errorf("CacheRef = %q", got)
	}
}

func TestNewHarborSchemeNormalization(t *testing.T) {
	for _, in := range []string{"harbor.dada-tuda.ru", "https://harbor.dada-tuda.ru/", "http://harbor.dada-tuda.ru"} {
		h := NewHarbor(in, "u", "p")
		if h.Host() != "harbor.dada-tuda.ru" {
			t.Errorf("Host(%q) = %q", in, h.Host())
		}
	}
}
