package models

import (
	"fmt"
	"testing"

	"github.com/vendas/loja/database"
)

// avaliacoesFalsas grava n avaliações com a nota dada para o vendedor.
func avaliacoesFalsas(t *testing.T, vendedor, n, estrelas int) {
	t.Helper()
	for i := 0; i < n; i++ {
		res, err := database.DB.Exec(`INSERT INTO customers (name, email, password_hash) VALUES ('C', ?, 'x')`,
			fmt.Sprintf("c%d-%d@exemplo.com", vendedor, i))
		if err != nil {
			t.Fatal(err)
		}
		cliente, _ := res.LastInsertId()
		res, err = database.DB.Exec(`INSERT INTO orders (customer_id, customer_name, customer_email, address, total, status, seller_id)
			VALUES (?, 'C', 'c@e.com', 'Rua', 10, 'entregue', ?)`, cliente, vendedor)
		if err != nil {
			t.Fatal(err)
		}
		pedido, _ := res.LastInsertId()
		if _, err := database.DB.Exec(`INSERT INTO seller_reviews (order_id, seller_id, customer_id, stars) VALUES (?, ?, ?, ?)`,
			pedido, vendedor, cliente, estrelas); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUmaNotaCincoNaoDaDescontoNaComissao(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	novato := vendedorConectado(t, "ana", cpfTeste1, "111")
	avaliacoesFalsas(t, novato, 1, 5)

	c := GetSellerCommission(novato)
	if c.Rating.Tier.Key != "esmeralda" {
		t.Fatalf("selo = %s; uma nota 5 já dá Esmeralda no selo", c.Rating.Tier.Key)
	}
	if c.Effective != 10 || c.Discount != 0 || c.MissingReviews != MinRatedSalesForDiscount-1 {
		t.Fatalf("comissão = %+v; desconto sem o mínimo de vendas avaliadas", c)
	}
}

func TestDescontoNaComissaoPorNivel(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ouro := vendedorConectado(t, "ouro", cpfTeste1, "111")
	avaliacoesFalsas(t, ouro, MinRatedSalesForDiscount, 4) // 4 estrelas não chega ao Ouro
	if c := GetSellerCommission(ouro); c.Rating.Tier.Key != "prata" || c.Effective != 10 {
		t.Fatalf("prata = %+v", c)
	}
	esmeralda := vendedorConectado(t, "esme", cpfTeste2, "222")
	avaliacoesFalsas(t, esmeralda, MinRatedSalesForDiscount, 5)
	if c := GetSellerCommission(esmeralda); c.Effective != 6 || c.Discount != 4 {
		t.Fatalf("esmeralda = %+v; quer 10%% - 4 = 6%%", c)
	}

	// O desconto entra no pedido: é a comissão que o Mercado Pago vai reter.
	o := pedidoCom(produtoDe(t, esmeralda, 100))
	if _, err := CreateOrder(o); err != nil {
		t.Fatal(err)
	}
	if o.MarketplaceFeeCents != 600 {
		t.Fatalf("comissão do pedido = %d centavos; quer 600", o.MarketplaceFeeCents)
	}

	// Desconto configurável, e nunca deixa a comissão negativa.
	SetSetting("comissao_percentual", "3")
	SetSetting(TierDiscountSettingKey("esmeralda"), "5")
	if c := GetSellerCommission(esmeralda); c.Effective != 0 {
		t.Fatalf("comissão negativa: %+v", c)
	}
}
