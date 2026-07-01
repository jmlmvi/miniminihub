package worker

import "testing"

func TestSingleDomain(t *testing.T) {
	if d, err := singleDomain([]string{"a@example.com", "b@example.com"}); err != nil || d != "example.com" {
		t.Fatalf("singleDomain ok: got (%q,%v)", d, err)
	}
	if _, err := singleDomain([]string{"a@example.com", "b@other.com"}); err == nil {
		t.Error("multi-domaines devrait échouer")
	}
	if _, err := singleDomain(nil); err == nil {
		t.Error("liste vide devrait échouer")
	}
	if _, err := singleDomain([]string{"invalide"}); err == nil {
		t.Error("adresse sans @ devrait échouer")
	}
	// Casse insensible sur le domaine.
	if d, _ := singleDomain([]string{"A@Example.COM"}); d != "example.com" {
		t.Errorf("domaine non normalisé: %q", d)
	}
}

func TestIsPermanent(t *testing.T) {
	if !isPermanent("550") {
		t.Error("550 devrait être permanent")
	}
	if isPermanent("451") {
		t.Error("451 (4xx) devrait être temporaire")
	}
	if isPermanent("") {
		t.Error("code vide = temporaire (DEFERRED)")
	}
}

func TestSmtpErrCodeExtraction(t *testing.T) {
	code, _, err := smtpErr(&textErr{"550 5.1.1 no such user"})
	if err == nil || code != "550" {
		t.Fatalf("code extrait = %q (err=%v)", code, err)
	}
	if c, _, _ := smtpErr(&textErr{"connection reset"}); c != "" {
		t.Errorf("erreur non-SMTP devrait donner un code vide, got %q", c)
	}
}

type textErr struct{ s string }

func (e *textErr) Error() string { return e.s }
