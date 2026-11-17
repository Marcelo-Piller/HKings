package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const clienteGoogle = "123-abc.apps.googleusercontent.com"

func tokenFalso(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(payload) + "." + enc([]byte("assinatura"))
}

func claimsValidas(agora time.Time) map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": clienteGoogle, "sub": "10987654321",
		"exp": agora.Add(time.Hour).Unix(), "nonce": "n-123",
		"email": "Leitora@Gmail.com", "email_verified": true, "name": "Ana Leitora",
	}
}

func TestIDTokenValido(t *testing.T) {
	agora := time.Now()
	id, err := ParseGoogleIDToken(tokenFalso(t, claimsValidas(agora)), clienteGoogle, "n-123", agora)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "10987654321" || id.Email != "leitora@gmail.com" || !id.EmailVerified || id.Name != "Ana Leitora" {
		t.Fatalf("identidade = %+v", id)
	}
}

func TestIDTokenRecusado(t *testing.T) {
	agora := time.Now()
	casos := map[string]func(map[string]any){
		"outro aplicativo": func(c map[string]any) { c["aud"] = "outro.apps.googleusercontent.com" },
		"outro emissor":    func(c map[string]any) { c["iss"] = "https://evil.example.com" },
		"vencido":          func(c map[string]any) { c["exp"] = agora.Add(-time.Minute).Unix() },
		"nonce trocado":    func(c map[string]any) { c["nonce"] = "de-outro-login" },
		"sem e-mail":       func(c map[string]any) { delete(c, "email") },
		"sem conta":        func(c map[string]any) { c["sub"] = "" },
	}
	for nome, estraga := range casos {
		c := claimsValidas(agora)
		estraga(c)
		if _, err := ParseGoogleIDToken(tokenFalso(t, c), clienteGoogle, "n-123", agora); !errors.Is(err, ErrGoogleIDToken) {
			t.Errorf("%s: aceito (err = %v)", nome, err)
		}
	}
	if _, err := ParseGoogleIDToken("nao-e-um-token", clienteGoogle, "n-123", agora); !errors.Is(err, ErrGoogleIDToken) {
		t.Errorf("token malformado aceito")
	}
}

func TestEmailNaoVerificadoVemMarcado(t *testing.T) {
	agora := time.Now()
	c := claimsValidas(agora)
	c["email_verified"] = "false"
	id, err := ParseGoogleIDToken(tokenFalso(t, c), clienteGoogle, "n-123", agora)
	if err != nil || id.EmailVerified {
		t.Fatalf("email_verified = %v, err = %v", id, err)
	}
}

func TestURLDeLoginUsaPKCE(t *testing.T) {
	u, err := url.Parse(GoogleAuthorizationURL(clienteGoogle, "https://loja/conta/google/retorno", "st", "nn", "verificador"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != PKCEChallenge("verificador") ||
		strings.Contains(u.String(), "verificador") {
		t.Fatalf("PKCE incorreto: %s", u)
	}
	if q.Get("state") != "st" || q.Get("nonce") != "nn" || q.Get("scope") != "openid email profile" {
		t.Fatalf("parâmetros: %v", q)
	}
}
