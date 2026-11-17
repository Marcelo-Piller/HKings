package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vendas/loja/database"
	"github.com/vendas/loja/models"
)

// O comprovante tem nome, e-mail e endereço do comprador: só quem fez o
// pedido pode baixá-lo, e só depois do pagamento confirmado.
func TestComprovanteSoParaQuemComprouEDepoisDePago(t *testing.T) {
	path := filepath.Join(t.TempDir(), "teste.db")
	t.Setenv("DB_PATH", path)
	database.Init(path)
	t.Cleanup(func() { database.DB.Close() })

	res, err := database.DB.Exec(`INSERT INTO products (name, price, stock, category, image_url, item_condition) VALUES ('Dom Casmurro', 39.9, 5, 'Geral', '/static/img/placeholder.svg', 'novo')`)
	if err != nil {
		t.Fatal(err)
	}
	produto, _ := res.LastInsertId()
	pedido := &models.Order{CustomerName: "Ana", CustomerEmail: "ana@exemplo.com", Address: "Rua 1",
		Items: []models.OrderItem{{ProductID: int(produto), Quantity: 1}}}
	id, err := models.CreateOrder(pedido)
	if err != nil {
		t.Fatal(err)
	}
	url := "/pedido/comprovante?id=" + strconv.FormatInt(id, 10)

	// Sessão de quem comprou (visitante sem conta).
	prep := httptest.NewRecorder()
	rememberGuestOrder(prep, httptest.NewRequest(http.MethodGet, "/", nil), int(id))
	comprador := prep.Result().Cookies()

	baixar := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		OrderReceiptPDF(w, r)
		return w
	}

	if w := baixar(nil); w.Code != http.StatusNotFound {
		t.Fatalf("estranho baixou o comprovante: HTTP %d", w.Code)
	}
	if w := baixar(comprador); w.Code != http.StatusSeeOther {
		t.Fatalf("comprovante de pedido não pago: HTTP %d; quer redirecionar", w.Code)
	}

	if _, err := models.MarkOrderPaid(int(id), 3990, "pix"); err != nil {
		t.Fatal(err)
	}
	w := baixar(comprador)
	if w.Code != http.StatusOK || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("comprador não baixou o PDF: HTTP %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("PDF com dados pessoais pode ficar em cache: %q", cc)
	}
	if w := baixar(nil); w.Code != http.StatusNotFound {
		t.Fatalf("estranho baixou comprovante de pedido pago: HTTP %d", w.Code)
	}
}

func TestFormatarDataEmHorarioDeBrasilia(t *testing.T) {
	// O banco grava em UTC; o comprovante mostra o horário de Brasília.
	if got := formatarData("2026-09-25 17:26:50"); got != "25/09/2026 às 14:26" {
		t.Fatalf("formatarData = %q", got)
	}
	if got := formatarData(""); got != "" {
		t.Fatalf("data vazia virou %q", got)
	}
}
