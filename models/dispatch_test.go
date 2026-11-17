package models

import (
	"testing"
	"time"

	"github.com/vendas/loja/database"
)

func dataBR(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", s, fusoBR)
	return t
}

func TestDiasUteis(t *testing.T) {
	casos := []struct {
		de   string
		dias int
		ate  string
	}{
		{"2026-10-02 15:00", 2, "2026-10-06 15:00"}, // sexta → terça
		{"2026-10-03 10:00", 2, "2026-10-06 10:00"}, // sábado → terça
		{"2026-10-09 09:00", 2, "2026-10-14 09:00"}, // sexta, 12/10 é feriado → quarta
		{"2026-04-02 12:00", 1, "2026-04-06 12:00"}, // quinta, Sexta-feira Santa → segunda
		{"2026-11-19 12:00", 1, "2026-11-23 12:00"}, // quinta, 20/11 (Consciência Negra) → segunda
	}
	for _, c := range casos {
		if got := AddBusinessDays(dataBR(c.de), c.dias); !got.Equal(dataBR(c.ate)) {
			t.Errorf("%s + %d dias úteis = %s; quer %s", c.de, c.dias, got.Format("2006-01-02 15:04 Mon"), c.ate)
		}
	}
	if IsBusinessDay(dataBR("2026-02-16 10:00")) { // segunda de Carnaval
		t.Error("Carnaval contado como dia útil")
	}
}

func TestPrazoPorTipoDeVendedor(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	sebo := vendedorConectado(t, "sebo", cpfTeste1, "111")
	leitor := vendedorConectado(t, "leitor", cpfTeste2, "222")
	SetSellerProfile(sebo, "sebo")
	SetSellerProfile(leitor, "leitor")
	if SLAHours(sebo) != 48 || SLAHours(leitor) != 72 || SLAHours(0) != 48 {
		t.Fatalf("prazos = %d, %d, %d", SLAHours(sebo), SLAHours(leitor), SLAHours(0))
	}
	SetSetting("sla_loja_horas", "24")
	if SLAHours(sebo) != 24 {
		t.Fatal("prazo configurado ignorado")
	}
}

// pedidoPago cria um pedido do vendedor pago em `pago` e postado em `postado`
// (vazio = ainda não postado).
func pedidoPagoEm(t *testing.T, vendedor int, pago time.Time, postado time.Time) int {
	t.Helper()
	livro := produtoDe(t, vendedor, 30)
	id, err := CreateOrder(&Order{CustomerName: "C", CustomerEmail: "c@ex.com", Address: "Rua 1", Items: []OrderItem{{ProductID: livro, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := MarkOrderPaid(int(id), 3000, "pix"); !ok || err != nil {
		t.Fatalf("pagamento: %v", err)
	}
	database.DB.Exec(`UPDATE orders SET paid_at=?, ship_deadline='' WHERE id=?`, dbTime(pago), id)
	SetShipDeadline(int(id))
	if !postado.IsZero() {
		if err := MarkOrderPosted(int(id), postado); err != nil {
			t.Fatal(err)
		}
	}
	return int(id)
}

func TestDespachoMedalhaEPausa(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	rapido := vendedorConectado(t, "rapido", cpfTeste1, "111")
	lento := vendedorConectado(t, "lento", cpfTeste2, "222")
	SetSellerProfile(rapido, "sebo")
	SetSellerProfile(lento, "sebo")
	agora := time.Now()
	base := agora.AddDate(0, 0, -20)
	for !IsBusinessDay(base) {
		base = base.AddDate(0, 0, 1)
	}
	base = time.Date(base.Year(), base.Month(), base.Day(), 9, 0, 0, 0, fusoBR)

	// Rápido: posta no mesmo dia.
	var primeiro int
	for i := 0; i < 4; i++ {
		id := pedidoPagoEm(t, rapido, base, base.Add(3*time.Hour))
		if primeiro == 0 {
			primeiro = id
		}
	}
	// Nota 5: nível Esmeralda. Sem avaliação, ficaria abaixo dos livros do site.
	leitora, _ := CreateCustomer("Leitora", "leitora@ex.com", "senha-com-12-caracteres")
	if _, err := database.DB.Exec(`INSERT INTO seller_reviews (order_id, seller_id, customer_id, stars) VALUES (?,?,?,5)`, primeiro, rapido, leitora); err != nil {
		t.Fatal(err)
	}
	s := GetShippingStats(rapido)
	if s.Shipped != 4 || s.SameDay != 4 || !s.FastShipper() || s.ReducedExposure() {
		t.Fatalf("rápido = %+v", s)
	}

	// Passos: pronto → postado; postar depois do prazo marca atraso.
	id := pedidoPagoEm(t, lento, base, time.Time{})
	if err := MarkOrderReady(id); err != nil {
		t.Fatal(err)
	}
	if err := MarkOrderReady(id); err == nil {
		t.Fatal("pronto duas vezes")
	}
	MarkOrderPosted(id, base.AddDate(0, 0, 6))
	if d := GetDispatch(id); !d.Late || d.ReadyAt.IsZero() {
		t.Fatalf("despacho = %+v", d)
	}
	if o, _ := GetOrder(id); o.Status != "enviado" {
		t.Fatalf("situação = %s", o.Status)
	}
	// Pedido parado há muito tempo: pausa.
	pedidoPagoEm(t, lento, base, time.Time{})
	s = GetShippingStats(lento)
	if s.OverdueNow != 1 || s.WorstOverdue <= 3 || !s.ShouldPause() || !s.ReducedExposure() {
		t.Fatalf("lento = %+v", s)
	}
	if len(ListOverdueOrders()) != 1 {
		t.Fatal("pedido atrasado fora da lista do admin")
	}
	if ok, _ := PauseSeller(lento, "teste"); !ok {
		t.Fatal("não pausou")
	}
	livros, _ := ListProducts("", "")
	for _, p := range livros {
		if p.SellerID == lento {
			t.Fatal("livro de loja pausada na vitrine")
		}
	}
	// Ranking: o rápido vem antes do site, que vem antes de quem atrasa.
	ReactivateSeller(lento)
	database.DB.Exec(`UPDATE sellers SET penalty_reset_at='2000-01-01 00:00:00' WHERE id=?`, lento)
	ForgetShippingBadges()
	livros, _ = ListProducts("", "")
	if len(livros) == 0 || livros[0].SellerID != rapido || !livros[0].FastShipper || livros[len(livros)-1].SellerID != lento {
		ordem := []int{}
		for _, p := range livros {
			ordem = append(ordem, p.SellerID)
		}
		t.Fatalf("ordem da vitrine por vendedor = %v (rápido %d, lento %d)", ordem, rapido, lento)
	}
	// Reativar zera o histórico.
	ReactivateSeller(lento)
	if s := GetShippingStats(lento); s.Shipped != 0 || s.OverdueNow != 0 {
		t.Fatalf("histórico não zerou: %+v", s)
	}
}
