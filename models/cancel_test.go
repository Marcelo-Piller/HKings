package models

import (
	"errors"
	"testing"

	"github.com/vendas/loja/database"
)

func estoque(t *testing.T, id int) int {
	t.Helper()
	var n int
	database.DB.QueryRow(`SELECT stock FROM products WHERE id=?`, id).Scan(&n)
	return n
}

func TestCancelarDevolveEstoqueUmaVez(t *testing.T) {
	setupDB(t)
	livro := produtoDe(t, 0, 30) // estoque 10
	o := pedidoCom(livro)
	o.Items[0].Quantity = 3
	id, err := CreateOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	if estoque(t, livro) != 7 {
		t.Fatalf("estoque depois da compra = %d", estoque(t, livro))
	}
	if err := UpdateOrderStatus(int(id), "cancelado"); err != nil {
		t.Fatal(err)
	}
	if estoque(t, livro) != 10 {
		t.Fatalf("estoque depois de cancelar = %d; quer 10", estoque(t, livro))
	}
	// Cancelar de novo não soma estoque, e o pedido não volta.
	UpdateOrderStatus(int(id), "cancelado")
	if estoque(t, livro) != 10 {
		t.Fatalf("cancelar duas vezes somou estoque: %d", estoque(t, livro))
	}
	if err := UpdateOrderStatus(int(id), "pago"); !errors.Is(err, ErrOrderCancelled) {
		t.Fatalf("pedido cancelado reaberto: %v", err)
	}
	// Pagamento atrasado não reabre o pedido cancelado.
	if ok, _ := MarkOrderPaid(int(id), 9000, "pix"); ok {
		t.Fatal("pedido cancelado marcado como pago")
	}
}

func TestPedidoSemPagamentoExpira(t *testing.T) {
	setupDB(t)
	livro := produtoDe(t, 0, 30)
	velho, _ := CreateOrder(pedidoCom(livro))
	novo, _ := CreateOrder(pedidoCom(livro))
	database.DB.Exec(`UPDATE orders SET created_at=datetime('now','-80 hours') WHERE id=?`, velho)
	pago, _ := CreateOrder(pedidoCom(livro))
	MarkOrderPaid(int(pago), 3000, "pix")
	database.DB.Exec(`UPDATE orders SET created_at=datetime('now','-80 hours') WHERE id=?`, pago)

	n, err := ExpireUnpaidOrders(72)
	if err != nil || n != 1 {
		t.Fatalf("expirados = %d, %v; quer só o pedido velho sem pagamento", n, err)
	}
	if g, _ := GetOrder(int(velho)); g.Status != "cancelado" {
		t.Fatalf("pedido velho = %s", g.Status)
	}
	if g, _ := GetOrder(int(novo)); g.Status == "cancelado" {
		t.Fatal("pedido recente cancelado")
	}
	if g, _ := GetOrder(int(pago)); g.Status != "pago" {
		t.Fatal("pedido pago cancelado")
	}
	if estoque(t, livro) != 8 { // 10 - 3 compras + 1 devolvida
		t.Fatalf("estoque = %d; quer 8", estoque(t, livro))
	}
}
