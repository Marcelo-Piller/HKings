package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

func TestCheckPaymentMatchesOrder(t *testing.T) {
	const vendedor = int64(111)
	pedidoVendedor := &models.Order{ID: 1, SellerID: 5, MarketplaceFeeCents: 799}
	pedidoSite := &models.Order{ID: 2, SellerID: 0}

	casos := []struct {
		nome   string
		pag    services.PaymentDetails
		pedido *models.Order
		quer   error
	}{
		{"vendedor certo, comissão certa", services.PaymentDetails{CollectorID: vendedor, ApplicationFeeCents: 799}, pedidoVendedor, nil},
		{"arredondamento de 1 centavo", services.PaymentDetails{CollectorID: vendedor, ApplicationFeeCents: 798}, pedidoVendedor, nil},
		// Um vendedor cria na própria conta um pagamento apontando para o
		// pedido de outro: o dinheiro não chegou a quem vendeu.
		{"recebido em outra conta", services.PaymentDetails{CollectorID: 999, ApplicationFeeCents: 799}, pedidoVendedor, errWrongCollector},
		// Pagamento feito fora do site, sem a comissão.
		{"sem comissão", services.PaymentDetails{CollectorID: vendedor}, pedidoVendedor, errFeeMissing},
		{"comissão menor", services.PaymentDetails{CollectorID: vendedor, ApplicationFeeCents: 100}, pedidoVendedor, errFeeMissing},
		{"venda do site dispensa comissão", services.PaymentDetails{CollectorID: vendedor}, pedidoSite, nil},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := checkPaymentMatchesOrder(&c.pag, c.pedido, vendedor)
			if c.quer == nil && err != nil {
				t.Fatalf("recusou pagamento válido: %v", err)
			}
			if c.quer != nil && !errors.Is(err, c.quer) {
				t.Fatalf("err = %v; quer %v", err, c.quer)
			}
		})
	}

	// Sem recebedor conhecido, nada é liberado.
	if err := checkPaymentMatchesOrder(&services.PaymentDetails{CollectorID: 0}, pedidoSite, 0); !errors.Is(err, errWrongCollector) {
		t.Fatalf("liberou pagamento sem recebedor conhecido: %v", err)
	}
}

func TestWebhookLeUserIDDoCorpo(t *testing.T) {
	corpo := `{"action":"payment.updated","type":"payment","data":{"id":"123"},"user_id":44556677}`
	r := httptest.NewRequest(http.MethodPost, "/webhook/mp", strings.NewReader(corpo))
	n, err := parseWebhookNotification(r)
	if err != nil {
		t.Fatal(err)
	}
	if n.UserID != "44556677" || n.DataID != "123" || n.Topic != "payment" {
		t.Fatalf("notificação lida errado: %+v", n)
	}
}

func TestOAuthRetornoRecusaStateAusenteOuTrocado(t *testing.T) {
	// Sem state na sessão: alguém tentando fazer o vendedor "conectar" uma
	// conta que não é dele.
	r := httptest.NewRequest(http.MethodGet, "/vendedor/mercadopago/retorno?code=abc&state=qualquer", nil)
	w := httptest.NewRecorder()
	SellerMPCallback(w, r)
	if loc := w.Header().Get("Location"); loc != "/vendedor?mp=erro" {
		t.Fatalf("retorno sem state aceito; Location = %q", loc)
	}

	// Com state na sessão, mas outro valor na URL.
	prep := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, _ := store.Get(req, "session")
	sess.Values[oauthStateKey] = "state-certo-da-sessao"
	if err := sess.Save(req, prep); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/vendedor/mercadopago/retorno?code=abc&state=state-errado-atacante", nil)
	for _, c := range prep.Result().Cookies() {
		r.AddCookie(c)
	}
	w = httptest.NewRecorder()
	SellerMPCallback(w, r)
	if loc := w.Header().Get("Location"); loc != "/vendedor?mp=erro" {
		t.Fatalf("state trocado aceito; Location = %q", loc)
	}
}
