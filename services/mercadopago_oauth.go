package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Split de pagamentos (modo marketplace).
//
// Cada vendedor autoriza o aplicativo do Livredo a cobrar em nome dele,
// pelo fluxo OAuth do Mercado Pago. Com o token do vendedor, a cobrança é
// criada na conta dele e o Mercado Pago separa sozinho a comissão do site
// (application_fee / marketplace_fee). O dinheiro do vendedor nunca passa pela
// conta do site — o que manteria o site na posição de guardar valores de
// terceiros, atividade regulada.

const mpAuthorizeURL = "https://auth.mercadopago.com.br/authorization"

// SellerTokens é o que o Mercado Pago devolve quando o vendedor autoriza o
// aplicativo, ou quando o token é renovado.
type SellerTokens struct {
	AccessToken  string
	RefreshToken string
	UserID       string // ID da conta do vendedor no Mercado Pago
	ExpiresAt    time.Time
}

type oauthTokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	UserID       json.RawMessage `json:"user_id"`
	ExpiresIn    int64           `json:"expires_in"`
	Message      string          `json:"message"`
	Error        string          `json:"error"`
}

// AuthorizationURL monta o endereço para onde o vendedor é mandado para
// autorizar o aplicativo. state amarra a volta à sessão de quem começou o
// fluxo, impedindo que alguém conecte a própria conta ao vendedor de outro.
func AuthorizationURL(clientID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("platform_id", "mp")
	q.Set("state", state)
	q.Set("redirect_uri", redirectURI)
	return mpAuthorizeURL + "?" + q.Encode()
}

// ExchangeAuthorizationCode troca o código recebido na volta do OAuth pelos
// tokens do vendedor.
func ExchangeAuthorizationCode(clientID, clientSecret, code, redirectURI string) (*SellerTokens, error) {
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("código de autorização ausente")
	}
	return oauthToken(map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
	})
}

// RefreshSellerToken renova o token do vendedor antes que ele expire (o
// Mercado Pago emite tokens válidos por 180 dias).
func RefreshSellerToken(clientID, clientSecret, refreshToken string) (*SellerTokens, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refresh token ausente")
	}
	return oauthToken(map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	})
}

func oauthToken(form map[string]string) (*SellerTokens, error) {
	if form["client_id"] == "" || form["client_secret"] == "" {
		return nil, fmt.Errorf("Client ID e Client Secret do Mercado Pago não configurados")
	}
	body, err := json.Marshal(form)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, mpBaseURL+"/oauth/token", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := mpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro ao chamar Mercado Pago: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed oauthTokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("resposta inválida do Mercado Pago: %w", err)
	}
	if resp.StatusCode >= 400 || parsed.AccessToken == "" {
		motivo := firstNonEmptyString(parsed.Message, parsed.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return nil, fmt.Errorf("Mercado Pago recusou a autorização: %s", motivo)
	}

	userID := strings.Trim(string(parsed.UserID), `"`)
	if _, err := strconv.ParseInt(userID, 10, 64); err != nil {
		return nil, fmt.Errorf("Mercado Pago não informou a conta do vendedor")
	}
	expiresIn := time.Duration(parsed.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = 180 * 24 * time.Hour
	}
	return &SellerTokens{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		UserID:       userID,
		ExpiresAt:    time.Now().Add(expiresIn),
	}, nil
}

// GetUserID devolve o ID da conta dona do token. Serve para saber qual é a
// conta do próprio site, e conferir que um pagamento de um livro do site foi
// mesmo recebido nela.
func GetUserID(accessToken string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, mpBaseURL+"/users/me", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := mpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("Mercado Pago retornou %d em /users/me", resp.StatusCode)
	}
	var me struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me); err != nil {
		return 0, err
	}
	if me.ID == 0 {
		return 0, fmt.Errorf("Mercado Pago não informou o ID da conta")
	}
	return me.ID, nil
}

// MarketplaceFee calcula a comissão do site sobre o total, em reais, com
// arredondamento para o centavo.
func MarketplaceFee(total float64, percent float64) float64 {
	if total <= 0 || percent <= 0 {
		return 0
	}
	return roundCurrency(total * percent / 100)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
