package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signManifest(t *testing.T, secret, dataID, requestID string, ts int64) string {
	t.Helper()
	manifest := fmt.Sprintf("id:%s;request-id:%s;ts:%d;", dataID, requestID, ts)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(manifest))
	return "ts=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignatureAceitaAssinaturaLegitima(t *testing.T) {
	const secret = "segredo-do-webhook"
	const dataID = "123456789"
	const requestID = "req-abc"
	now := time.Now().Unix()

	header := signManifest(t, secret, dataID, requestID, now)
	if err := VerifyWebhookSignature(secret, header, requestID, dataID); err != nil {
		t.Fatalf("assinatura legítima recusada: %v", err)
	}
}

func TestVerifyWebhookSignatureRejeitaFalsificacao(t *testing.T) {
	const dataID = "123456789"
	const requestID = "req-abc"
	now := time.Now().Unix()

	// Quem não conhece o segredo não consegue produzir a assinatura: é esta
	// checagem que impede marcar pedidos como pagos via webhook forjado.
	header := signManifest(t, "segredo-errado", dataID, requestID, now)
	if err := VerifyWebhookSignature("segredo-certo", header, requestID, dataID); err == nil {
		t.Fatal("assinatura forjada foi aceita")
	}
}

func TestVerifyWebhookSignatureRejeitaTrocaDeIdentificador(t *testing.T) {
	const secret = "segredo-do-webhook"
	const requestID = "req-abc"
	now := time.Now().Unix()

	// Assinatura válida para um pagamento não pode ser reaproveitada em outro.
	header := signManifest(t, secret, "111", requestID, now)
	if err := VerifyWebhookSignature(secret, header, requestID, "222"); err == nil {
		t.Fatal("assinatura de outro pagamento foi aceita")
	}
}

func TestVerifyWebhookSignatureRejeitaNotificacaoAntiga(t *testing.T) {
	const secret = "segredo-do-webhook"
	const dataID = "123456789"
	const requestID = "req-abc"
	antigo := time.Now().Add(-2 * time.Hour).Unix()

	header := signManifest(t, secret, dataID, requestID, antigo)
	if err := VerifyWebhookSignature(secret, header, requestID, dataID); err == nil {
		t.Fatal("notificação antiga foi aceita; reenvio de requisição capturada seria possível")
	}
}

func TestVerifyWebhookSignatureExigeSegredoECabecalho(t *testing.T) {
	now := time.Now().Unix()
	header := signManifest(t, "s", "1", "r", now)

	if err := VerifyWebhookSignature("", header, "r", "1"); err == nil {
		t.Fatal("aceitou verificação sem segredo configurado")
	}
	if err := VerifyWebhookSignature("s", "", "r", "1"); err == nil {
		t.Fatal("aceitou requisição sem cabeçalho x-signature")
	}
	if err := VerifyWebhookSignature("s", "ts=abc", "r", "1"); err == nil {
		t.Fatal("aceitou cabeçalho malformado")
	}
}

func TestExternalReferenceIdaEVolta(t *testing.T) {
	reference := ExternalReference(4321)
	if reference != "pedido-4321" {
		t.Fatalf("referência = %q", reference)
	}
	id, err := ParseExternalReference(reference)
	if err != nil || id != 4321 {
		t.Fatalf("ParseExternalReference = %d, %v", id, err)
	}
	if _, err := ParseExternalReference("outra-coisa-9"); err == nil {
		t.Fatal("aceitou referência de formato desconhecido")
	}
}

func TestStatementDescriptorSanitiza(t *testing.T) {
	// O Mercado Pago só aceita letras e números, com tamanho limitado.
	if got := statementDescriptor("Minha Loja Ótima!"); got != "MINHALOJATIMA" {
		t.Fatalf("descriptor = %q", got)
	}
	if got := statementDescriptor("Loja Muito Grande Demais"); len(got) > 13 {
		t.Fatalf("descriptor longo demais: %q", got)
	}
	// Nome longo perde as primeiras palavras em vez de ser cortado no meio.
	if got := statementDescriptor("Sebo Casa Amarela"); got != "CASAAMARELA" {
		t.Fatalf("descriptor = %q; quer CASAAMARELA", got)
	}
	if got := statementDescriptor("Livredo"); got != "LIVREDO" {
		t.Fatalf("descriptor = %q; quer LIVREDO", got)
	}
	if got := statementDescriptor("Supercalifragilisticexpialidoce"); got != "SUPERCALIFRAG" {
		t.Fatalf("palavra única longa = %q", got)
	}
}

func TestCreatePixPaymentValidaEntradas(t *testing.T) {
	if _, err := CreatePixPayment("", 10, "d", 1, "a@b.c", "", time.Minute, 0); err == nil {
		t.Fatal("aceitou criar cobrança sem access token")
	}
	if _, err := CreatePixPayment("token", 0, "d", 1, "a@b.c", "", time.Minute, 0); err == nil {
		t.Fatal("aceitou criar cobrança de valor zero")
	}
}

func TestCreateCheckoutPreferenceValidaEntradas(t *testing.T) {
	items := []PaymentItem{{Title: "Produto", Quantity: 1, UnitPrice: 10}}

	if _, err := CreateCheckoutPreference("", items, 1, "N", "a@b.c", "https://x.com", "", "Loja", 12, time.Hour, 0); err == nil {
		t.Fatal("aceitou preferência sem access token")
	}
	if _, err := CreateCheckoutPreference("token", nil, 1, "N", "a@b.c", "https://x.com", "", "Loja", 12, time.Hour, 0); err == nil {
		t.Fatal("aceitou preferência sem itens")
	}
	if _, err := CreateCheckoutPreference("token", items, 1, "N", "a@b.c", "", "", "Loja", 12, time.Hour, 0); err == nil {
		t.Fatal("aceitou preferência sem URL base, que o retorno do checkout exige")
	}
	invalidos := []PaymentItem{{Title: "Produto", Quantity: 0, UnitPrice: 10}}
	if _, err := CreateCheckoutPreference("token", invalidos, 1, "N", "a@b.c", "https://x.com", "", "Loja", 12, time.Hour, 0); err == nil {
		t.Fatal("aceitou item com quantidade zero")
	}
}

func TestPaymentDetailsApproved(t *testing.T) {
	// "authorized" é apenas reserva de limite no cartão e não pode liberar o
	// pedido; só "approved" significa dinheiro liquidado.
	if (PaymentDetails{Status: "authorized"}).Approved() {
		t.Fatal("status authorized tratado como pago")
	}
	if !(PaymentDetails{Status: "approved"}).Approved() {
		t.Fatal("status approved não reconhecido")
	}
}

func TestCreatePixPaymentRecusaComissaoInvalida(t *testing.T) {
	// Comissão igual ou maior que o valor deixaria o vendedor sem nada.
	if _, err := CreatePixPayment("token", 10, "d", 1, "a@b.c", "", time.Minute, 10); err == nil {
		t.Fatal("aceitou comissão igual ao valor")
	}
	if _, err := CreatePixPayment("token", 10, "d", 1, "a@b.c", "", time.Minute, -1); err == nil {
		t.Fatal("aceitou comissão negativa")
	}
}

func TestMarketplaceFee(t *testing.T) {
	casos := []struct{ total, pct, quer float64 }{
		{100, 10, 10},
		{79.9, 10, 7.99},
		{33.33, 10, 3.33},
		{50, 0, 0},
	}
	for _, c := range casos {
		if got := MarketplaceFee(c.total, c.pct); got != c.quer {
			t.Errorf("MarketplaceFee(%v, %v) = %v; quer %v", c.total, c.pct, got, c.quer)
		}
	}
}

func TestAuthorizationURLLevaState(t *testing.T) {
	u := AuthorizationURL("123", "https://loja.com/vendedor/mercadopago/retorno", "estado-aleatorio")
	for _, parte := range []string{"client_id=123", "state=estado-aleatorio", "response_type=code", "redirect_uri=https%3A%2F%2Floja.com%2Fvendedor%2Fmercadopago%2Fretorno"} {
		if !strings.Contains(u, parte) {
			t.Errorf("URL de autorização sem %q: %s", parte, u)
		}
	}
}
