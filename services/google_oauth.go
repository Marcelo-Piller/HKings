package services

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Login de clientes com a conta Google (OpenID Connect, fluxo de código com
// PKCE). O site só pede nome e e-mail: escopos "openid email profile".

const (
	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
)

var googleClient = &http.Client{Timeout: 15 * time.Second}

var ErrGoogleIDToken = errors.New("identidade do Google inválida")

// GoogleIdentity é o que o site usa da conta Google.
type GoogleIdentity struct {
	Subject       string // identificador fixo da conta Google
	Email         string
	EmailVerified bool
	Name          string
}

// PKCEChallenge deriva o code_challenge (S256) do verificador guardado na sessão.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GoogleAuthorizationURL monta o endereço da tela de login do Google.
// state amarra a volta à sessão que começou o login; nonce amarra o ID token
// a este login; o code_challenge impede que um código interceptado seja
// trocado por outra pessoa.
func GoogleAuthorizationURL(clientID, redirectURI, state, nonce, verifier string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", PKCEChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "select_account")
	return googleAuthorizeURL + "?" + q.Encode()
}

// ExchangeGoogleCode troca o código da volta do login pelo ID token e devolve
// a identidade já conferida.
func ExchangeGoogleCode(clientID, clientSecret, code, redirectURI, verifier, nonce string) (*GoogleIdentity, error) {
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("código de autorização ausente")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURI)

	resp, err := googleClient.PostForm(googleTokenURL, form)
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}
	var token struct {
		IDToken          string `json:"id_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("google: resposta inválida (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || token.IDToken == "" {
		return nil, fmt.Errorf("google: HTTP %d: %s %s", resp.StatusCode, token.Error, token.ErrorDescription)
	}
	return ParseGoogleIDToken(token.IDToken, clientID, nonce, time.Now())
}

// ParseGoogleIDToken lê e confere o ID token.
//
// A assinatura não é verificada de propósito: o token chega direto do
// endpoint de token do Google, numa conexão HTTPS aberta pelo próprio
// servidor, e nunca passa pelo navegador. Nesse caso a especificação do
// OpenID Connect (Core, 3.1.3.7) e a documentação do Google aceitam o TLS
// como garantia da origem. Emissor, destinatário, validade e nonce continuam
// sendo conferidos.
func ParseGoogleIDToken(idToken, clientID, nonce string, now time.Time) (*GoogleIdentity, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: formato", ErrGoogleIDToken)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: conteúdo", ErrGoogleIDToken)
	}
	var claims struct {
		Issuer        string          `json:"iss"`
		Audience      json.RawMessage `json:"aud"`
		Subject       string          `json:"sub"`
		Expires       int64           `json:"exp"`
		Nonce         string          `json:"nonce"`
		Email         string          `json:"email"`
		EmailVerified json.RawMessage `json:"email_verified"`
		Name          string          `json:"name"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: conteúdo", ErrGoogleIDToken)
	}
	if claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com" {
		return nil, fmt.Errorf("%w: emissor %q", ErrGoogleIDToken, claims.Issuer)
	}
	if !audienceContains(claims.Audience, clientID) {
		return nil, fmt.Errorf("%w: emitido para outro aplicativo", ErrGoogleIDToken)
	}
	if claims.Expires == 0 || now.Unix() >= claims.Expires {
		return nil, fmt.Errorf("%w: vencido", ErrGoogleIDToken)
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return nil, fmt.Errorf("%w: nonce", ErrGoogleIDToken)
	}
	if claims.Subject == "" || claims.Email == "" {
		return nil, fmt.Errorf("%w: sem conta ou e-mail", ErrGoogleIDToken)
	}
	// email_verified vem como booleano, mas algumas contas antigas o mandam
	// como texto "true".
	verified := strings.Trim(string(claims.EmailVerified), `"`) == "true"
	return &GoogleIdentity{
		Subject:       claims.Subject,
		Email:         strings.ToLower(strings.TrimSpace(claims.Email)),
		EmailVerified: verified,
		Name:          strings.TrimSpace(claims.Name),
	}, nil
}

func audienceContains(raw json.RawMessage, clientID string) bool {
	if clientID == "" {
		return false
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == clientID {
				return true
			}
		}
	}
	return false
}
