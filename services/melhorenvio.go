package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Melhor Envio (API v2): cotação de frete no checkout e geração de etiqueta
// pelo vendedor.
//
// Cada vendedor conecta a própria conta (OAuth) e a etiqueta é paga com o
// saldo da conta dele: o frete cobrado do comprador vai para o vendedor junto
// com o valor dos livros, no split do Mercado Pago.

const (
	meProdURL    = "https://melhorenvio.com.br"
	meSandboxURL = "https://sandbox.melhorenvio.com.br"

	// meScopes são as permissões pedidas na conexão da conta.
	meScopes = "cart-read cart-write companies-read orders-read shipping-calculate shipping-cancel " +
		"shipping-checkout shipping-companies shipping-generate shipping-preview shipping-print " +
		"shipping-share shipping-tracking users-read"

	// MEServices são as modalidades oferecidas: Correios PAC, SEDEX e Mini
	// Envios. Os Correios não exigem escolher agência de postagem, ao
	// contrário de outras transportadoras.
	MEServices = "1,2,17"
)

var meClient = &http.Client{Timeout: 20 * time.Second}

// ErrMEUnauthorized: o token venceu ou foi revogado; é preciso reconectar.
var ErrMEUnauthorized = errors.New("conta Melhor Envio desconectada ou sem permissão")

// MEConfig identifica o aplicativo da loja no Melhor Envio.
type MEConfig struct {
	ClientID     string
	ClientSecret string
	Sandbox      bool
	// Contact vai no User-Agent, que o Melhor Envio exige com um e-mail de
	// contato técnico.
	Contact string
}

// MEBaseURLOverride substitui o endereço do Melhor Envio: nos testes, e pela
// variável MELHORENVIO_API_URL para apontar um ambiente de homologação.
var MEBaseURLOverride = os.Getenv("MELHORENVIO_API_URL")

func (c MEConfig) baseURL() string {
	if MEBaseURLOverride != "" {
		return MEBaseURLOverride
	}
	if c.Sandbox {
		return meSandboxURL
	}
	return meProdURL
}

func (c MEConfig) userAgent() string {
	if c.Contact == "" {
		return "Livredo"
	}
	return "Livredo (" + c.Contact + ")"
}

// ── OAuth ───────────────────────────────────────────────────────────────────

// METokens são os tokens de uma conta conectada.
type METokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// MEAuthorizationURL leva o vendedor ao Melhor Envio para autorizar a loja.
func MEAuthorizationURL(cfg MEConfig, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("scope", meScopes)
	return cfg.baseURL() + "/oauth/authorize?" + q.Encode()
}

// MEExchangeCode troca o código da volta da autorização pelos tokens.
func MEExchangeCode(cfg MEConfig, code, redirectURI string) (*METokens, error) {
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("código de autorização ausente")
	}
	return meToken(cfg, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"redirect_uri":  {redirectURI},
		"code":          {code},
	})
}

// MERefresh renova os tokens (o de acesso vale 30 dias).
func MERefresh(cfg MEConfig, refreshToken string) (*METokens, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refresh token ausente")
	}
	return meToken(cfg, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"refresh_token": {refreshToken},
	})
}

func meToken(cfg MEConfig, form url.Values) (*METokens, error) {
	req, err := http.NewRequest(http.MethodPost, cfg.baseURL()+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cfg.userAgent())
	resp, err := meClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("melhor envio: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Message      string `json:"message"`
	}
	json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return nil, fmt.Errorf("melhor envio: token HTTP %d: %s %s", resp.StatusCode, out.Error, out.Message)
	}
	return &METokens{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
	}, nil
}

// ── Chamadas autenticadas ───────────────────────────────────────────────────

// MEAPI faz chamadas em nome de uma conta conectada.
type MEAPI struct {
	Config MEConfig
	Token  string
	// BaseURL substitui o endereço da API (usado nos testes).
	BaseURL string
}

func (a MEAPI) do(method, path string, payload, out any) error {
	base := a.BaseURL
	if base == "" {
		base = a.Config.baseURL()
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, base+"/api/v2/me"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("User-Agent", a.Config.userAgent())
	resp, err := meClient.Do(req)
	if err != nil {
		return fmt.Errorf("melhor envio: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrMEUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("melhor envio: HTTP %d: %s", resp.StatusCode, meErrorMessage(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("melhor envio: resposta inesperada: %w", err)
		}
	}
	return nil
}

// meErrorMessage extrai a mensagem legível de um erro da API, que vem como
// {"message": "...", "errors": {"campo": ["..."]}}.
func meErrorMessage(raw []byte) string {
	var e struct {
		Message string              `json:"message"`
		Error   string              `json:"error"`
		Errors  map[string][]string `json:"errors"`
	}
	if json.Unmarshal(raw, &e) != nil {
		if len(raw) > 200 {
			raw = raw[:200]
		}
		return string(raw)
	}
	partes := []string{}
	if e.Message != "" {
		partes = append(partes, e.Message)
	} else if e.Error != "" {
		partes = append(partes, e.Error)
	}
	campos := make([]string, 0, len(e.Errors))
	for campo := range e.Errors {
		campos = append(campos, campo)
	}
	sort.Strings(campos)
	for _, campo := range campos {
		partes = append(partes, strings.Join(e.Errors[campo], " "))
	}
	return strings.Join(partes, " — ")
}

// ── Cotação ─────────────────────────────────────────────────────────────────

// MEPackageItem é um livro na cotação: medidas em cm, peso em kg.
type MEPackageItem struct {
	ID             string  `json:"id"`
	Width          float64 `json:"width"`
	Height         float64 `json:"height"`
	Length         float64 `json:"length"`
	Weight         float64 `json:"weight"`
	InsuranceValue float64 `json:"insurance_value"`
	Quantity       int     `json:"quantity"`
}

// MEQuote é uma opção de frete.
type MEQuote struct {
	ServiceID  int
	Name       string // "SEDEX"
	Company    string // "Correios"
	PriceCents int64
	Days       int
}

// Label é o nome exibido: "Correios SEDEX".
func (q MEQuote) Label() string {
	return strings.TrimSpace(q.Company + " " + q.Name)
}

// meMoney aceita preço como texto ("23.71") ou número (23.71): a API usa os
// dois formatos.
type meMoney float64

func (m *meMoney) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*m = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*m = meMoney(v)
	return nil
}

// Calculate cota o frete entre dois CEPs. Modalidades indisponíveis para o
// trecho (que a API devolve com "error") ficam de fora.
func (a MEAPI) Calculate(fromCEP, toCEP string, items []MEPackageItem) ([]MEQuote, error) {
	payload := map[string]any{
		"from":     map[string]string{"postal_code": fromCEP},
		"to":       map[string]string{"postal_code": toCEP},
		"products": items,
		"options":  map[string]bool{"receipt": false, "own_hand": false},
		"services": MEServices,
	}
	var raw []struct {
		ID           int     `json:"id"`
		Name         string  `json:"name"`
		Price        meMoney `json:"price"`
		CustomPrice  meMoney `json:"custom_price"`
		DeliveryTime int     `json:"delivery_time"`
		CustomTime   int     `json:"custom_delivery_time"`
		Error        string  `json:"error"`
		Company      struct {
			Name string `json:"name"`
		} `json:"company"`
	}
	if err := a.do(http.MethodPost, "/shipment/calculate", payload, &raw); err != nil {
		return nil, err
	}
	var quotes []MEQuote
	for _, q := range raw {
		price := float64(q.CustomPrice)
		if price <= 0 {
			price = float64(q.Price)
		}
		if q.Error != "" || price <= 0 {
			continue
		}
		days := q.CustomTime
		if days == 0 {
			days = q.DeliveryTime
		}
		quotes = append(quotes, MEQuote{
			ServiceID:  q.ID,
			Name:       q.Name,
			Company:    q.Company.Name,
			PriceCents: int64(price*100 + 0.5),
			Days:       days,
		})
	}
	sort.Slice(quotes, func(i, j int) bool { return quotes[i].PriceCents < quotes[j].PriceCents })
	return quotes, nil
}

// ── Etiqueta ────────────────────────────────────────────────────────────────

// MEAddress é remetente ou destinatário da etiqueta.
type MEAddress struct {
	Name            string `json:"name"`
	Phone           string `json:"phone,omitempty"`
	Email           string `json:"email,omitempty"`
	Document        string `json:"document,omitempty"`         // CPF
	CompanyDocument string `json:"company_document,omitempty"` // CNPJ
	Address         string `json:"address"`
	Number          string `json:"number"`
	Complement      string `json:"complement,omitempty"`
	District        string `json:"district"`
	City            string `json:"city"`
	StateAbbr       string `json:"state_abbr"`
	CountryID       string `json:"country_id"`
	PostalCode      string `json:"postal_code"`
}

// MELabelRequest é tudo que a etiqueta precisa.
type MELabelRequest struct {
	ServiceID      int
	From, To       MEAddress
	Products       []MELabelProduct
	Volume         MEVolume
	InsuranceValue float64
	// InvoiceKey é a chave da NF-e. Vazia, o envio usa declaração de conteúdo.
	InvoiceKey string
	Tag        string // aparece no painel do Melhor Envio, ex.: "Livredo #12"
}

type MELabelProduct struct {
	Name         string  `json:"name"`
	Quantity     int     `json:"quantity"`
	UnitaryValue float64 `json:"unitary_value"`
}

// MEVolume é o pacote: medidas em cm, peso em kg.
type MEVolume struct {
	Height float64 `json:"height"`
	Width  float64 `json:"width"`
	Length float64 `json:"length"`
	Weight float64 `json:"weight"`
}

// MELabel é o resultado da geração.
type MELabel struct {
	OrderID  string // pedido no Melhor Envio
	PrintURL string // PDF para imprimir
	Tracking string // código de rastreio (pode vir vazio até a postagem)
}

// GenerateLabel faz o caminho completo: coloca no carrinho, compra com o
// saldo da conta, gera e devolve o link de impressão. Se a compra falhar (por
// exemplo, saldo insuficiente), o item fica no carrinho do Melhor Envio e o
// erro volta para o vendedor resolver lá.
func (a MEAPI) GenerateLabel(req MELabelRequest) (*MELabel, error) {
	options := map[string]any{
		"insurance_value": req.InsuranceValue,
		"receipt":         false,
		"own_hand":        false,
		"reverse":         false,
		"non_commercial":  req.InvoiceKey == "",
		"platform":        "Livredo",
		"tags":            []map[string]any{{"tag": req.Tag, "url": nil}},
	}
	if req.InvoiceKey != "" {
		options["invoice"] = map[string]string{"key": req.InvoiceKey}
	}
	payload := map[string]any{
		"service":  req.ServiceID,
		"from":     req.From,
		"to":       req.To,
		"products": req.Products,
		"volumes":  []MEVolume{req.Volume},
		"options":  options,
	}
	var cart struct {
		ID string `json:"id"`
	}
	if err := a.do(http.MethodPost, "/cart", payload, &cart); err != nil {
		return nil, fmt.Errorf("carrinho: %w", err)
	}
	if cart.ID == "" {
		return nil, fmt.Errorf("melhor envio: carrinho sem id")
	}
	orders := map[string][]string{"orders": {cart.ID}}
	if err := a.do(http.MethodPost, "/shipment/checkout", orders, nil); err != nil {
		return &MELabel{OrderID: cart.ID}, fmt.Errorf("compra da etiqueta: %w", err)
	}
	if err := a.do(http.MethodPost, "/shipment/generate", orders, nil); err != nil {
		return &MELabel{OrderID: cart.ID}, fmt.Errorf("geração da etiqueta: %w", err)
	}
	label := &MELabel{OrderID: cart.ID}
	label.PrintURL, _ = a.PrintURL(cart.ID)
	label.Tracking, _ = a.Tracking(cart.ID)
	return label, nil
}

// PrintURL devolve o link público do PDF da etiqueta.
func (a MEAPI) PrintURL(meOrderID string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	err := a.do(http.MethodPost, "/shipment/print", map[string]any{"mode": "public", "orders": []string{meOrderID}}, &out)
	return out.URL, err
}

// Tracking devolve o código de rastreio da etiqueta, se já houver.
func (a MEAPI) Tracking(meOrderID string) (string, error) {
	var out map[string]struct {
		Tracking            *string `json:"tracking"`
		MelhorEnvioTracking *string `json:"melhorenvio_tracking"`
	}
	if err := a.do(http.MethodPost, "/shipment/tracking", map[string][]string{"orders": {meOrderID}}, &out); err != nil {
		return "", err
	}
	if t, ok := out[meOrderID]; ok {
		if t.Tracking != nil && *t.Tracking != "" {
			return *t.Tracking, nil
		}
		if t.MelhorEnvioTracking != nil {
			return *t.MelhorEnvioTracking, nil
		}
	}
	return "", nil
}

// METracking é o andamento de uma etiqueta no Melhor Envio.
type METracking struct {
	Status      string // pending, released, posted, delivered, canceled, undelivered
	Code        string // código de rastreio (Correios ou transportadora)
	PostedAt    time.Time
	DeliveredAt time.Time
}

// TrackingStatus consulta status, código e datas de postagem e entrega.
func (a MEAPI) TrackingStatus(meOrderID string) (*METracking, error) {
	var out map[string]struct {
		Status              string  `json:"status"`
		Tracking            *string `json:"tracking"`
		MelhorEnvioTracking *string `json:"melhorenvio_tracking"`
		PostedAt            *string `json:"posted_at"`
		DeliveredAt         *string `json:"delivered_at"`
	}
	if err := a.do(http.MethodPost, "/shipment/tracking", map[string][]string{"orders": {meOrderID}}, &out); err != nil {
		return nil, err
	}
	t, ok := out[meOrderID]
	if !ok {
		return nil, fmt.Errorf("etiqueta %s não encontrada no Melhor Envio", meOrderID)
	}
	res := &METracking{Status: t.Status}
	if t.Tracking != nil && *t.Tracking != "" {
		res.Code = *t.Tracking
	} else if t.MelhorEnvioTracking != nil {
		res.Code = *t.MelhorEnvioTracking
	}
	parse := func(v *string) time.Time {
		if v == nil || *v == "" {
			return time.Time{}
		}
		// O Melhor Envio devolve a hora de Brasília, sem fuso.
		for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
			if tm, err := time.ParseInLocation(layout, *v, brasilia); err == nil {
				return tm
			}
		}
		return time.Time{}
	}
	res.PostedAt, res.DeliveredAt = parse(t.PostedAt), parse(t.DeliveredAt)
	return res, nil
}

var brasilia = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("BRT", -3*60*60)
}()

// ── CEP ─────────────────────────────────────────────────────────────────────

// CEPAddress é o endereço de um CEP.
type CEPAddress struct {
	Street   string `json:"rua"`
	District string `json:"bairro"`
	City     string `json:"cidade"`
	State    string `json:"uf"`
}

// ViaCEPURL é o endereço do ViaCEP (substituído nos testes).
var ViaCEPURL = "https://viacep.com.br/ws/%s/json/"

var cepClient = &http.Client{Timeout: 6 * time.Second}

// LookupCEP consulta o ViaCEP para preencher o endereço no checkout. O CEP
// precisa ter 8 dígitos.
func LookupCEP(cep string) (*CEPAddress, error) {
	if len(cep) != 8 {
		return nil, fmt.Errorf("CEP inválido")
	}
	resp, err := cepClient.Get(fmt.Sprintf(ViaCEPURL, cep))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Logradouro string `json:"logradouro"`
		Bairro     string `json:"bairro"`
		Localidade string `json:"localidade"`
		UF         string `json:"uf"`
		Erro       any    `json:"erro"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, err
	}
	if out.Erro != nil || out.Localidade == "" {
		return nil, fmt.Errorf("CEP não encontrado")
	}
	return &CEPAddress{Street: out.Logradouro, District: out.Bairro, City: out.Localidade, State: out.UF}, nil
}
