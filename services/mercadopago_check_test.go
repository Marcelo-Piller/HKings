package services

import "testing"

func TestClassifyMPToken(t *testing.T) {
	casos := map[string]string{
		"APP_USR-1234567890123456-100226-0123456789abcdef0123456789abcdef-123456789": "producao",
		"TEST-1234567890123456-100226-0123456789abcdef0123456789abcdef-123456789":    "teste",
		"APP_USR-0123abcd-0123-4567-89ab-0123456789ab":                               "public_key",
		"minhaSenha123": "desconhecido",
		"":              "vazio",
	}
	for v, quer := range casos {
		if got := ClassifyMPToken(v).Tipo; got != quer {
			t.Errorf("%q = %s; quer %s", v, got, quer)
		}
	}
}

func TestValidMPWebhookSecret(t *testing.T) {
	if !ValidMPWebhookSecret("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") {
		t.Error("segredo válido recusado")
	}
	for _, v := range []string{"minhaSenha123", "APP_USR-1234567890123456-100226-0123456789abcdef0123456789abcdef-123456789"} {
		if ValidMPWebhookSecret(v) {
			t.Errorf("%q aceito", v)
		}
	}
}
