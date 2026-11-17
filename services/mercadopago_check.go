package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Diagnóstico do Access Token do Mercado Pago, para o botão "Testar conexão"
// das Configurações. Nada aqui devolve o token: só o tipo, o formato e a
// resposta da conta.

// MPTokenKind descreve o formato do que foi salvo como Access Token.
type MPTokenKind struct {
	Tipo     string // "producao", "teste", "public_key", "client_secret", "desconhecido", "vazio"
	Mensagem string
	Espacos  bool // espaço, tabulação ou quebra de linha dentro do valor
	Tamanho  int
}

var (
	// APP_USR-<16 dígitos>-<6 dígitos>-<32 hex>-<id do usuário>
	reAccessToken = regexp.MustCompile(`^(APP_USR|TEST)-\d{10,20}-\d{6}-[0-9a-f]{32}-\d{5,15}$`)
	// A Public Key também começa com APP_USR (ou TEST), mas tem formato de UUID.
	rePublicKey    = regexp.MustCompile(`^(APP_USR|TEST)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reClientSecret = regexp.MustCompile(`^[A-Za-z0-9]{24,40}$`)
)

// ClassifyMPToken identifica o que foi colado no campo do Access Token.
func ClassifyMPToken(token string) MPTokenKind {
	k := MPTokenKind{Tamanho: len(token), Espacos: strings.ContainsAny(token, " \t\r\n")}
	t := strings.TrimSpace(token)
	switch {
	case t == "":
		k.Tipo, k.Mensagem = "vazio", "Nenhum Access Token salvo."
	case rePublicKey.MatchString(t):
		k.Tipo, k.Mensagem = "public_key", "Isto é a Public Key, não o Access Token. No painel do Mercado Pago, copie o campo \"Access Token\" (o mais longo, que termina com números)."
	case reAccessToken.MatchString(t) && strings.HasPrefix(t, "TEST-"):
		k.Tipo, k.Mensagem = "teste", "Access Token de TESTE: não cobra de verdade e só aceita compradores de teste."
	case reAccessToken.MatchString(t):
		k.Tipo, k.Mensagem = "producao", "Access Token de produção, no formato certo."
	case reClientSecret.MatchString(t):
		k.Tipo, k.Mensagem = "client_secret", "Isto parece o Client Secret, não o Access Token."
	default:
		k.Tipo, k.Mensagem = "desconhecido", "O valor salvo não tem o formato de um Access Token do Mercado Pago (APP_USR-…)."
	}
	return k
}

// MPAccount é a conta dona do token.
type MPAccount struct {
	ID       int64  `json:"id"`
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
	SiteID   string `json:"site_id"`
}

// MPWhoAmI consulta a conta do token em /users/me e devolve o erro do
// Mercado Pago por extenso quando ele recusa.
func MPWhoAmI(accessToken string) (*MPAccount, error) {
	req, err := http.NewRequest(http.MethodGet, mpBaseURL+"/users/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	resp, err := mpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sem conexão com o Mercado Pago: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		json.Unmarshal(body, &e)
		return nil, fmt.Errorf("Mercado Pago recusou o token (HTTP %d): %s %s", resp.StatusCode, e.Error, e.Message)
	}
	var acc MPAccount
	if err := json.Unmarshal(body, &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// A assinatura secreta do webhook do Mercado Pago é um código hexadecimal
// (64 caracteres hoje); aceita de 32 a 128 para não quebrar se mudar.
var reWebhookSecret = regexp.MustCompile(`^[0-9a-fA-F]{32,128}$`)

// ValidMPWebhookSecret diz se o valor tem o formato da assinatura secreta.
func ValidMPWebhookSecret(s string) bool {
	return reWebhookSecret.MatchString(strings.TrimSpace(s))
}
