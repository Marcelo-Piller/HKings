package handlers

import (
	"os"
	"strings"
	"testing"

	"github.com/vendas/loja/models"
)

func TestSoMercadoPagoEhDestinoDoCheckout(t *testing.T) {
	validos := []string{
		"https://www.mercadopago.com.br/checkout/v1/redirect?pref_id=123",
		"https://sandbox.mercadopago.com.br/checkout/v1/redirect?pref_id=123",
	}
	for _, u := range validos {
		if !isMercadoPagoCheckoutURL(u) {
			t.Errorf("recusou %s", u)
		}
	}
	invalidos := []string{
		"", "http://www.mercadopago.com.br/checkout", "https://evil.com/?mercadopago.com.br",
		"https://mercadopago.com.br.evil.com/", "https://evilmercadopago.com.br/", "javascript:alert(1)",
	}
	for _, u := range invalidos {
		if isMercadoPagoCheckoutURL(u) {
			t.Errorf("aceitou %s", u)
		}
	}
}

func TestPaginaIntermediariaLevaAoMercadoPago(t *testing.T) {
	original, _ := os.Getwd()
	if err := os.Chdir(".."); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original) })
	Init()

	checkout := "https://www.mercadopago.com.br/checkout/v1/redirect?pref_id=123-abc&x=1"
	var b strings.Builder
	err := pages["payment_redirect.html"].ExecuteTemplate(&b, "layout", map[string]any{
		"Order":       &models.Order{ID: 42},
		"CheckoutURL": checkout,
		"CartCount":   0,
		"SelectedCat": "", "Estado": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := b.String()
	// O html/template troca por "ZgotmplZ" valores que considera inseguros em
	// certos atributos; a meta refresh precisa sair com o endereço intacto.
	want := `content="0;url=https://www.mercadopago.com.br/checkout/v1/redirect?pref_id=123-abc&amp;x=1"`
	if !strings.Contains(html, want) {
		i := strings.Index(html, `http-equiv="refresh"`)
		t.Fatalf("meta refresh incorreta: %s", html[max(i, 0):min(len(html), max(i, 0)+220)])
	}
}
