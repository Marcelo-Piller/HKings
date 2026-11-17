package services

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// meFalso imita a API do Melhor Envio e registra o que recebeu.
type meFalso struct {
	chamadas []string
	corpos   map[string]map[string]any
	headers  http.Header
	// falharEm faz o caminho indicado responder com erro.
	falharEm string
}

func (m *meFalso) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.chamadas = append(m.chamadas, r.URL.Path)
	m.headers = r.Header.Clone()
	raw, _ := io.ReadAll(r.Body)
	var corpo map[string]any
	json.Unmarshal(raw, &corpo)
	if m.corpos == nil {
		m.corpos = map[string]map[string]any{}
	}
	m.corpos[r.URL.Path] = corpo
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == m.falharEm {
		w.WriteHeader(422)
		io.WriteString(w, `{"message":"Saldo insuficiente.","errors":{"balance":["Adicione saldo para continuar."]}}`)
		return
	}
	switch r.URL.Path {
	case "/api/v2/me/shipment/calculate":
		io.WriteString(w, `[
			{"id":2,"name":"SEDEX","price":"31.40","custom_price":"31.40","delivery_time":2,"custom_delivery_time":2,"company":{"name":"Correios"}},
			{"id":1,"name":"PAC","price":19.9,"delivery_time":6,"company":{"name":"Correios"}},
			{"id":17,"name":"Mini Envios","error":"Dimensões do pacote excedem o limite","company":{"name":"Correios"}}
		]`)
	case "/api/v2/me/cart":
		io.WriteString(w, `{"id":"9a1b-etiqueta","protocol":"ORD-1"}`)
	case "/api/v2/me/shipment/checkout", "/api/v2/me/shipment/generate":
		io.WriteString(w, `{}`)
	case "/api/v2/me/shipment/print":
		io.WriteString(w, `{"url":"https://melhorenvio.com.br/imprimir/abc"}`)
	case "/api/v2/me/shipment/tracking":
		io.WriteString(w, `{"9a1b-etiqueta":{"tracking":"AA123456789BR","melhorenvio_tracking":"ME123"}}`)
	default:
		w.WriteHeader(404)
	}
}

func apiFalsa(t *testing.T, m *meFalso) MEAPI {
	t.Helper()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return MEAPI{Config: MEConfig{Contact: "dev@livredo.com.br"}, Token: "tok-123", BaseURL: srv.URL}
}

func TestCotacaoIgnoraModalidadeIndisponivelEOrdenaPorPreco(t *testing.T) {
	m := &meFalso{}
	api := apiFalsa(t, m)
	quotes, err := api.Calculate("01310100", "20040002", []MEPackageItem{{ID: "1", Width: 16, Height: 4, Length: 23, Weight: 0.4, InsuranceValue: 39.9, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(quotes) != 2 || quotes[0].ServiceID != 1 || quotes[0].PriceCents != 1990 || quotes[1].PriceCents != 3140 {
		t.Fatalf("cotações = %+v", quotes)
	}
	if quotes[1].Label() != "Correios SEDEX" || quotes[1].Days != 2 {
		t.Fatalf("rótulo/prazo = %q %d", quotes[1].Label(), quotes[1].Days)
	}
	if m.headers.Get("Authorization") != "Bearer tok-123" || !strings.Contains(m.headers.Get("User-Agent"), "dev@livredo.com.br") {
		t.Fatalf("cabeçalhos = %v", m.headers)
	}
	corpo := m.corpos["/api/v2/me/shipment/calculate"]
	if corpo["services"] != MEServices || corpo["from"].(map[string]any)["postal_code"] != "01310100" {
		t.Fatalf("corpo da cotação = %v", corpo)
	}
}

func TestEtiquetaCaminhoCompleto(t *testing.T) {
	m := &meFalso{}
	api := apiFalsa(t, m)
	label, err := api.GenerateLabel(MELabelRequest{
		ServiceID: 2,
		From:      MEAddress{Name: "Sebo", Document: "52998224725", PostalCode: "01310100", CountryID: "BR"},
		To:        MEAddress{Name: "Leitora", Document: "11144477735", PostalCode: "20040002", CountryID: "BR"},
		Products:  []MELabelProduct{{Name: "Dom Casmurro", Quantity: 1, UnitaryValue: 39.9}},
		Volume:    MEVolume{Height: 4, Width: 16, Length: 23, Weight: 0.4},
		Tag:       "Livredo #7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if label.OrderID != "9a1b-etiqueta" || label.PrintURL != "https://melhorenvio.com.br/imprimir/abc" || label.Tracking != "AA123456789BR" {
		t.Fatalf("etiqueta = %+v", label)
	}
	ordem := strings.Join(m.chamadas, " → ")
	if ordem != "/api/v2/me/cart → /api/v2/me/shipment/checkout → /api/v2/me/shipment/generate → /api/v2/me/shipment/print → /api/v2/me/shipment/tracking" {
		t.Fatalf("ordem das chamadas: %s", ordem)
	}
	opcoes := m.corpos["/api/v2/me/cart"]["options"].(map[string]any)
	if opcoes["non_commercial"] != true || opcoes["invoice"] != nil {
		t.Fatalf("sem NF-e o envio deve ir com declaração de conteúdo: %v", opcoes)
	}
}

func TestEtiquetaComNotaFiscal(t *testing.T) {
	m := &meFalso{}
	api := apiFalsa(t, m)
	if _, err := api.GenerateLabel(MELabelRequest{ServiceID: 1, InvoiceKey: strings.Repeat("1", 44)}); err != nil {
		t.Fatal(err)
	}
	opcoes := m.corpos["/api/v2/me/cart"]["options"].(map[string]any)
	if opcoes["non_commercial"] != false || opcoes["invoice"].(map[string]any)["key"] != strings.Repeat("1", 44) {
		t.Fatalf("NF-e não foi enviada: %v", opcoes)
	}
}

func TestSaldoInsuficienteDevolveOPedidoNoCarrinho(t *testing.T) {
	m := &meFalso{falharEm: "/api/v2/me/shipment/checkout"}
	api := apiFalsa(t, m)
	label, err := api.GenerateLabel(MELabelRequest{ServiceID: 1})
	if err == nil || !strings.Contains(err.Error(), "Saldo insuficiente") || !strings.Contains(err.Error(), "Adicione saldo") {
		t.Fatalf("erro = %v", err)
	}
	if label == nil || label.OrderID != "9a1b-etiqueta" {
		t.Fatalf("o id do carrinho precisa voltar para o vendedor resolver lá: %+v", label)
	}
	for _, c := range m.chamadas {
		if c == "/api/v2/me/shipment/generate" {
			t.Fatal("gerou etiqueta sem ter comprado")
		}
	}
}

func TestTokenVencidoViraErroDeConexao(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Unauthenticated."}`)
	}))
	defer srv.Close()
	api := MEAPI{Token: "velho", BaseURL: srv.URL}
	if _, err := api.Calculate("01310100", "20040002", nil); !errors.Is(err, ErrMEUnauthorized) {
		t.Fatalf("erro = %v", err)
	}
}

func TestTrocaDoCodigoOAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path != "/oauth/token" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "abc" ||
			r.Form.Get("client_secret") != "segredo" {
			w.WriteHeader(400)
			return
		}
		io.WriteString(w, `{"token_type":"Bearer","expires_in":2592000,"access_token":"acc","refresh_token":"ref"}`)
	}))
	defer srv.Close()
	MEBaseURLOverride = srv.URL
	defer func() { MEBaseURLOverride = "" }()

	tok, err := MEExchangeCode(MEConfig{ClientID: "1", ClientSecret: "segredo"}, "abc", "https://livredo.com.br/melhorenvio/retorno")
	if err != nil || tok.AccessToken != "acc" || tok.RefreshToken != "ref" || tok.ExpiresAt.IsZero() {
		t.Fatalf("tokens = %+v, %v", tok, err)
	}
	if u := MEAuthorizationURL(MEConfig{ClientID: "1"}, "https://livredo.com.br/melhorenvio/retorno", "st"); !strings.Contains(u, "state=st") ||
		!strings.Contains(u, "shipping-generate") {
		t.Fatalf("URL de autorização = %s", u)
	}
}
