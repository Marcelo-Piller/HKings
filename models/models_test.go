package models

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vendas/loja/database"
)

// setupDB prepara um banco descartável por teste, com o mesmo esquema e os
// mesmos PRAGMAs que a aplicação usa em produção.
func setupDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "teste.db")
	t.Setenv("DB_PATH", path)
	database.Init(path)
	t.Cleanup(func() {
		if database.DB != nil {
			database.DB.Close()
		}
	})
}

func criaProduto(t *testing.T, preco float64, estoque int) int {
	t.Helper()
	res, err := database.DB.Exec(
		`INSERT INTO products (name, description, price, stock, image_url, category, active)
		 VALUES ('Produto Teste', '', ?, ?, '/static/img/placeholder.svg', 'Geral', 1)`,
		preco, estoque,
	)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func TestChavesEstrangeirasEstaoAtivas(t *testing.T) {
	setupDB(t)

	var foreignKeys int
	if err := database.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatal("foreign_keys desligado; a integridade referencial não seria aplicada")
	}

	// Com as chaves ativas, um item não pode apontar para um pedido inexistente.
	_, err := database.DB.Exec(
		`INSERT INTO order_items (order_id, product_id, product_name, quantity, price) VALUES (99999, 1, 'X', 1, 10)`,
	)
	if err == nil {
		t.Fatal("item órfão foi aceito; chave estrangeira não está sendo aplicada")
	}
}

func TestModoWALAtivo(t *testing.T) {
	setupDB(t)

	var journal string
	if err := database.DB.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q; esperado wal", journal)
	}
}

func TestMarkOrderPaidRecusaValorMenorQueOPedido(t *testing.T) {
	setupDB(t)
	produtoID := criaProduto(t, 100.00, 10)

	order := &Order{
		CustomerName: "Cliente", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: produtoID, Quantity: 2}},
	}
	id, err := CreateOrder(order)
	if err != nil {
		t.Fatal(err)
	}

	// Pedido de R$ 200,00 com pagamento aprovado de R$ 50,00 não pode liberar.
	changed, err := MarkOrderPaid(int(id), 5000, "pix")
	if !errors.Is(err, ErrUnderpaid) {
		t.Fatalf("pagamento parcial aceito; err = %v", err)
	}
	if changed {
		t.Fatal("pedido marcado como pago com valor insuficiente")
	}

	recuperado, err := GetOrder(int(id))
	if err != nil {
		t.Fatal(err)
	}
	if recuperado.Status == "pago" {
		t.Fatal("status avançou para pago indevidamente")
	}
}

func TestMarkOrderPaidEhIdempotente(t *testing.T) {
	setupDB(t)
	produtoID := criaProduto(t, 50.00, 10)

	order := &Order{
		CustomerName: "Cliente", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: produtoID, Quantity: 1}},
	}
	id, err := CreateOrder(order)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := MarkOrderPaid(int(id), 5000, "pix")
	if err != nil || !changed {
		t.Fatalf("primeira confirmação falhou: changed=%v err=%v", changed, err)
	}

	// O Mercado Pago reenvia webhooks; a segunda chamada não pode disparar a
	// notificação ao lojista de novo.
	changed, err = MarkOrderPaid(int(id), 5000, "pix")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("webhook repetido produziu nova transição de status")
	}
}

func TestCreateOrderNaoConfiaNoPrecoEnviado(t *testing.T) {
	setupDB(t)
	produtoID := criaProduto(t, 100.00, 10)

	// O preço vem do banco, não do carrinho: um preço forjado é ignorado.
	order := &Order{
		CustomerName: "Cliente", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: produtoID, Quantity: 1, Price: 0.01}},
	}
	id, err := CreateOrder(order)
	if err != nil {
		t.Fatal(err)
	}
	recuperado, err := GetOrder(int(id))
	if err != nil {
		t.Fatal(err)
	}
	if recuperado.Total != 100.00 {
		t.Fatalf("total = %.2f; o preço enviado pelo cliente foi usado", recuperado.Total)
	}
}

func TestCreateOrderRespeitaEstoque(t *testing.T) {
	setupDB(t)
	produtoID := criaProduto(t, 10.00, 2)

	order := &Order{
		CustomerName: "Cliente", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: produtoID, Quantity: 5}},
	}
	if _, err := CreateOrder(order); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("venda acima do estoque aceita; err = %v", err)
	}
}

func TestUpdateSellerOrderStatusNaoPermiteMarcarComoPago(t *testing.T) {
	setupDB(t)
	produtoID := criaProduto(t, 10.00, 5)

	order := &Order{
		CustomerName: "Cliente", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: produtoID, Quantity: 1}},
	}
	id, err := CreateOrder(order)
	if err != nil {
		t.Fatal(err)
	}

	// Confirmar pagamento é exclusivo do webhook do provedor.
	if err := UpdateSellerOrderStatus(int(id), "pago"); err == nil {
		t.Fatal("vendedor conseguiu marcar o pedido como pago")
	}
	// E não dá para pular do aguardando pagamento direto para enviado.
	if err := UpdateSellerOrderStatus(int(id), "enviado"); err == nil {
		t.Fatal("vendedor enviou pedido que ainda não foi pago")
	}
}

func TestValidateProductRecusaDadosInvalidos(t *testing.T) {
	setupDB(t)

	casos := []struct {
		nome    string
		produto Product
	}{
		{"preço negativo", Product{Name: "X", Price: -10, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: placeholderParaTeste}},
		{"preço zero", Product{Name: "X", Price: 0, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: placeholderParaTeste}},
		{"estoque negativo", Product{Name: "X", Price: 10, Stock: -5, Category: "Geral", Condition: ConditionNew, ImageURL: placeholderParaTeste}},
		{"nome vazio", Product{Name: "   ", Price: 10, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: placeholderParaTeste}},
		{"categoria inexistente", Product{Name: "X", Price: 10, Stock: 1, Category: "Inventada", Condition: ConditionNew, ImageURL: placeholderParaTeste}},
		{"imagem externa", Product{Name: "X", Price: 10, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: "https://exemplo.com/a.png"}},
		{"imagem com travessia", Product{Name: "X", Price: 10, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: "/static/img/../../etc/passwd"}},
		{"estado ausente", Product{Name: "X", Price: 10, Stock: 1, Category: "Geral", ImageURL: placeholderParaTeste}},
		{"estado inventado", Product{Name: "X", Price: 10, Stock: 1, Category: "Geral", Condition: "seminovo", ImageURL: placeholderParaTeste}},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if err := CreateProduct(caso.produto); !errors.Is(err, ErrInvalidProduct) {
				t.Fatalf("aceitou produto inválido; err = %v", err)
			}
		})
	}
}

const placeholderParaTeste = "/static/img/placeholder.svg"

func TestCategoriaNaoPodeSerInventada(t *testing.T) {
	setupDB(t)

	if !CategoryExists("Geral") {
		t.Fatal("categoria semeada não encontrada")
	}
	if CategoryExists("Categoria Que Nao Existe") {
		t.Fatal("categoria inexistente reportada como válida")
	}
}

func TestDeleteCategoryProtegeCategoriaEmUso(t *testing.T) {
	setupDB(t)
	criaProduto(t, 10, 1) // fica na categoria "Geral"

	categorias, err := ListCategoriesFull()
	if err != nil {
		t.Fatal(err)
	}
	var geralID int
	for _, c := range categorias {
		if c.Name == "Geral" {
			geralID = c.ID
		}
	}
	if geralID == 0 {
		t.Fatal("categoria Geral não encontrada")
	}

	if err := DeleteCategory(geralID); !errors.Is(err, ErrCategoryInUse) {
		t.Fatalf("excluiu categoria com produtos; err = %v", err)
	}
}

// CPF e CNPJ de teste válidos (dígito verificador correto), usados só para
// exercitar unicidade — não pertencem a ninguém real.
const (
	cpfTeste1  = "52998224725"
	cpfTeste2  = "11144477735"
	cnpjTeste1 = "11222333000181"
)

func TestCreateSellerRecusaUsuarioDuplicado(t *testing.T) {
	setupDB(t)

	if err := CreateSeller("Vendedor Um", "vendedor1", "um@exemplo.com", cpfTeste1, "senha-com-12-caracteres"); err != nil {
		t.Fatalf("primeiro cadastro falhou: %v", err)
	}
	// O autocadastro do vendedor depende dessa mensagem específica para não
	// vazar o erro cru do SQLite no formulário.
	err := CreateSeller("Outro Nome", "vendedor1", "dois@exemplo.com", cpfTeste2, "outra-senha-valida")
	if !errors.Is(err, ErrUsernameInUse) {
		t.Fatalf("usuário duplicado aceito; err = %v", err)
	}
}

func TestCreateSellerRecusaEmailDuplicado(t *testing.T) {
	setupDB(t)

	if err := CreateSeller("Vendedor Um", "vendedor1", "mesmo@exemplo.com", cpfTeste1, "senha-com-12-caracteres"); err != nil {
		t.Fatalf("primeiro cadastro falhou: %v", err)
	}
	err := CreateSeller("Outro Nome", "vendedor2", "mesmo@exemplo.com", cpfTeste2, "outra-senha-valida")
	if !errors.Is(err, ErrEmailInUse) {
		t.Fatalf("e-mail duplicado aceito; err = %v", err)
	}
}

func TestCreateSellerRecusaDocumentoDuplicado(t *testing.T) {
	setupDB(t)

	if err := CreateSeller("Vendedor Um", "vendedor1", "um@exemplo.com", cpfTeste1, "senha-com-12-caracteres"); err != nil {
		t.Fatalf("primeiro cadastro falhou: %v", err)
	}
	// Mesmo CPF com máscara diferente: a normalização precisa pegar isso.
	err := CreateSeller("Outro Nome", "vendedor2", "dois@exemplo.com", "529.982.247-25", "outra-senha-valida")
	if !errors.Is(err, ErrDocumentInUse) {
		t.Fatalf("CPF duplicado aceito mesmo com máscara diferente; err = %v", err)
	}
}

func TestCreateSellerRecusaEmailInvalido(t *testing.T) {
	setupDB(t)

	err := CreateSeller("Vendedor", "vendedor1", "isso-nao-e-email", cpfTeste1, "senha-com-12-caracteres")
	if !errors.Is(err, ErrInvalidSeller) {
		t.Fatalf("e-mail inválido aceito; err = %v", err)
	}
}

func TestCreateSellerRecusaDocumentoInvalido(t *testing.T) {
	setupDB(t)

	err := CreateSeller("Vendedor", "vendedor1", "v@exemplo.com", "111.111.111-11", "senha-com-12-caracteres")
	if !errors.Is(err, ErrInvalidSeller) {
		t.Fatalf("documento inválido aceito; err = %v", err)
	}
}

func TestCreateSellerAceitaCNPJ(t *testing.T) {
	setupDB(t)

	if err := CreateSeller("Empresa Vendedora", "empresa1", "empresa@exemplo.com", cnpjTeste1, "senha-com-12-caracteres"); err != nil {
		t.Fatalf("CNPJ válido recusado: %v", err)
	}
}

func TestSegredosNaoFicamEmTextoPuroNoBanco(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=") // 32 bytes
	// Garante que a leitura não venha da variável de ambiente do token.
	os.Unsetenv("MERCADOPAGO_ACCESS_TOKEN")

	const segredo = "APP_USR-token-super-secreto"
	if err := SetSetting("mp_access_token", segredo); err != nil {
		t.Fatal(err)
	}

	var gravado string
	if err := database.DB.QueryRow(`SELECT value FROM settings WHERE key='mp_access_token'`).Scan(&gravado); err != nil {
		t.Fatal(err)
	}
	if gravado == segredo {
		t.Fatal("token gravado em texto puro no banco")
	}

	if lido := GetSetting("mp_access_token"); lido != segredo {
		t.Fatalf("token não voltou corretamente: %q", lido)
	}
}

func TestListProductsFiltraPorEstadoEGenero(t *testing.T) {
	setupDB(t)
	// Isola do catálogo semeado: a contagem abaixo considera só estes três.
	if _, err := database.DB.Exec(`UPDATE products SET active=0`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Product{
		{Name: "Mangá novo", Price: 30, Stock: 1, Category: "Mangá", Condition: ConditionNew, ImageURL: placeholderParaTeste},
		{Name: "Mangá usado", Price: 15, Stock: 1, Category: "Mangá", Condition: ConditionUsed, ImageURL: placeholderParaTeste},
		{Name: "HQ usada", Price: 20, Stock: 1, Category: "HQ", Condition: ConditionUsed, ImageURL: placeholderParaTeste},
	} {
		if err := CreateProduct(p); err != nil {
			t.Fatalf("cadastro de %q: %v", p.Name, err)
		}
	}

	casos := []struct {
		genero, estado string
		quer           int
	}{
		{"", "", 3},
		{"", ConditionUsed, 2},
		{"", ConditionNew, 1},
		{"Mangá", "", 2},
		{"Mangá", ConditionUsed, 1},
		{"HQ", ConditionNew, 0},
		{"", "seminovo", 3}, // estado desconhecido não filtra, para não esvaziar a vitrine
	}
	for _, c := range casos {
		produtos, err := ListProducts(c.genero, c.estado)
		if err != nil {
			t.Fatal(err)
		}
		if len(produtos) != c.quer {
			t.Errorf("ListProducts(%q, %q) = %d produtos; quer %d", c.genero, c.estado, len(produtos), c.quer)
		}
	}
}

func TestMangaEHQExistemComoGeneros(t *testing.T) {
	setupDB(t)
	// Os atalhos do cabeçalho apontam para esses gêneros; o vendedor precisa
	// conseguir escolhê-los ao cadastrar.
	for _, genero := range []string{"Mangá", "HQ"} {
		if !CategoryExists(genero) {
			t.Errorf("gênero %q não existe", genero)
		}
	}
}

func TestProdutoAntigoSemEstadoNaoViraNovo(t *testing.T) {
	setupDB(t)
	// Produto anterior ao campo: a loja não pode anunciá-lo como novo.
	res, err := database.DB.Exec(`INSERT INTO products (name, price, stock, category, image_url) VALUES ('Antigo', 10, 1, 'Geral', ?)`, placeholderParaTeste)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	p, err := GetProduct(int(id))
	if err != nil {
		t.Fatal(err)
	}
	if p.Condition != "" || p.ConditionLabel() != "" {
		t.Fatalf("produto antigo ganhou estado presumido: %q", p.Condition)
	}
}

func TestDeleteProductApagaQuandoNuncaFoiVendido(t *testing.T) {
	setupDB(t)
	id := criaProduto(t, 10, 5)

	removed, _, err := DeleteProduct(id)
	if err != nil || !removed {
		t.Fatalf("produto sem vendas não foi apagado: removed=%v err=%v", removed, err)
	}
	var n int
	database.DB.QueryRow(`SELECT COUNT(*) FROM products WHERE id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("linha do produto continua no banco")
	}
}

func TestDeleteProductArquivaQuandoJaFoiVendido(t *testing.T) {
	setupDB(t)
	id := criaProduto(t, 10, 5)
	order := &Order{CustomerName: "C", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: id, Quantity: 1}}}
	pedido, err := CreateOrder(order)
	if err != nil {
		t.Fatal(err)
	}

	removed, _, err := DeleteProduct(id)
	if err != nil || removed {
		t.Fatalf("produto vendido deveria ser arquivado: removed=%v err=%v", removed, err)
	}
	// Some da loja e dos painéis...
	if _, err := GetProduct(id); err == nil {
		t.Fatal("produto arquivado ainda aparece")
	}
	todos, _ := ListAllProducts()
	for _, p := range todos {
		if p.ID == id {
			t.Fatal("produto arquivado ainda aparece na lista do admin")
		}
	}
	// ...mas o pedido antigo continua íntegro.
	o, err := GetOrder(int(pedido))
	if err != nil || len(o.Items) != 1 {
		t.Fatalf("histórico do pedido quebrou: %v", err)
	}
}

func TestCategoriaComSoProdutoArquivadoPodeSerExcluida(t *testing.T) {
	setupDB(t)
	database.DB.Exec(`UPDATE products SET archived=1`) // tira as sementes da conta
	id := criaProduto(t, 10, 5)
	order := &Order{CustomerName: "C", CustomerEmail: "c@exemplo.com", Address: "Rua 1",
		Items: []OrderItem{{ProductID: id, Quantity: 1}}}
	if _, err := CreateOrder(order); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DeleteProduct(id); err != nil {
		t.Fatal(err)
	}
	var geral int
	database.DB.QueryRow(`SELECT id FROM categories WHERE name='Geral'`).Scan(&geral)
	if err := DeleteCategory(geral); err != nil {
		t.Fatalf("categoria só com produto arquivado deveria sair: %v", err)
	}
}

func TestVendedorDoProduto(t *testing.T) {
	setupDB(t)
	if err := CreateSeller("Livraria Sebo", "sebo", "sebo@exemplo.com", cnpjTeste1, "senha-com-12-caracteres"); err != nil {
		t.Fatal(err)
	}
	if err := CreateSeller("Ana Leitora", "ana", "ana@exemplo.com", cpfTeste1, "senha-com-12-caracteres"); err != nil {
		t.Fatal(err)
	}
	var loja, pessoa int
	database.DB.QueryRow(`SELECT id FROM sellers WHERE username='sebo'`).Scan(&loja)
	database.DB.QueryRow(`SELECT id FROM sellers WHERE username='ana'`).Scan(&pessoa)

	casos := []struct {
		vendedor        int
		nome, tipo      string
		vendidoPeloSite bool
	}{
		{0, "Livredo", "", true},
		{loja, "Livraria Sebo", "Loja", false},
		{pessoa, "Ana Leitora", "Pessoa física", false},
	}
	for _, c := range casos {
		res, err := database.DB.Exec(`INSERT INTO products (seller_id, name, price, stock, category, image_url, item_condition) VALUES (?, 'X', 10, 1, 'Geral', ?, 'novo')`, c.vendedor, placeholderParaTeste)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		p, err := GetProduct(int(id))
		if err != nil {
			t.Fatal(err)
		}
		if p.SellerLabel() != c.nome || p.SellerKind() != c.tipo || p.SoldBySite() != c.vendidoPeloSite {
			t.Errorf("vendedor %d: got (%q, %q, %v); quer (%q, %q, %v)",
				c.vendedor, p.SellerLabel(), p.SellerKind(), p.SoldBySite(), c.nome, c.tipo, c.vendidoPeloSite)
		}
		// O produto vai para o cookie da sacola: o documento do vendedor não
		// pode estar em nenhum campo dele.
		if bruto := fmt.Sprintf("%+v", *p); strings.Contains(bruto, cpfTeste1) || strings.Contains(bruto, cnpjTeste1) {
			t.Fatalf("documento do vendedor vazou para o produto: %s", bruto)
		}
	}
}

// ── Split de pagamentos ─────────────────────────────────────────────────────

const chaveTeste = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func vendedorConectado(t *testing.T, usuario, documento, mpUser string) int {
	t.Helper()
	if err := CreateSeller("Vendedor "+usuario, usuario, usuario+"@exemplo.com", documento, "senha-com-12-caracteres"); err != nil {
		t.Fatal(err)
	}
	var id int
	database.DB.QueryRow(`SELECT id FROM sellers WHERE username=?`, usuario).Scan(&id)
	if mpUser != "" {
		if err := SaveSellerMPTokens(id, mpUser, "APP_USR-token-"+usuario, "refresh-"+usuario, time.Now().Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func produtoDe(t *testing.T, vendedor int, preco float64) int {
	t.Helper()
	res, err := database.DB.Exec(`INSERT INTO products (seller_id, name, price, stock, category, image_url, item_condition) VALUES (?, 'Livro', ?, 10, 'Geral', ?, 'novo')`, vendedor, preco, placeholderParaTeste)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func pedidoCom(produtos ...int) *Order {
	o := &Order{CustomerName: "C", CustomerEmail: "c@exemplo.com", Address: "Rua 1"}
	for _, p := range produtos {
		o.Items = append(o.Items, OrderItem{ProductID: p, Quantity: 1})
	}
	return o
}

func TestTokensDoVendedorFicamCriptografados(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	id := vendedorConectado(t, "ana", cpfTeste1, "111222")

	var gravado string
	database.DB.QueryRow(`SELECT mp_access_token FROM sellers WHERE id=?`, id).Scan(&gravado)
	if strings.Contains(gravado, "APP_USR") {
		t.Fatal("token do vendedor gravado em texto puro")
	}
	conta, err := GetSellerMPAccount(id)
	if err != nil || conta.AccessToken != "APP_USR-token-ana" || conta.UserID != "111222" {
		t.Fatalf("token não voltou: %+v, %v", conta, err)
	}
	if achado, err := SellerIDByMPUser("111222"); err != nil || achado != id {
		t.Fatalf("SellerIDByMPUser = %d, %v", achado, err)
	}
}

func TestComissaoSoEmVendaDeVendedor(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	vendedor := vendedorConectado(t, "ana", cpfTeste1, "111222")

	doSite := pedidoCom(produtoDe(t, 0, 50))
	if _, err := CreateOrder(doSite); err != nil {
		t.Fatal(err)
	}
	if doSite.SellerID != 0 || doSite.MarketplaceFeeCents != 0 {
		t.Fatalf("venda do site com comissão: seller=%d fee=%d", doSite.SellerID, doSite.MarketplaceFeeCents)
	}

	doVendedor := pedidoCom(produtoDe(t, vendedor, 79.90))
	if _, err := CreateOrder(doVendedor); err != nil {
		t.Fatal(err)
	}
	if doVendedor.SellerID != vendedor || doVendedor.MarketplaceFeeCents != 799 {
		t.Fatalf("venda do vendedor: seller=%d fee=%d; quer %d e 799", doVendedor.SellerID, doVendedor.MarketplaceFeeCents, vendedor)
	}
}

func TestPedidoNaoMisturaVendedores(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")
	bia := vendedorConectado(t, "bia", cpfTeste2, "222")

	if _, err := CreateOrder(pedidoCom(produtoDe(t, ana, 10), produtoDe(t, bia, 10))); !errors.Is(err, ErrMixedSellers) {
		t.Fatalf("pedido com dois vendedores aceito; err = %v", err)
	}
	if _, err := CreateOrder(pedidoCom(produtoDe(t, ana, 10), produtoDe(t, 0, 10))); !errors.Is(err, ErrMixedSellers) {
		t.Fatalf("pedido com vendedor e site aceito; err = %v", err)
	}
}

func TestVendedorSemContaNaoVende(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	semConta := vendedorConectado(t, "ana", cpfTeste1, "")
	produto := produtoDe(t, semConta, 10)

	if _, err := CreateOrder(pedidoCom(produto)); !errors.Is(err, ErrSellerCannotReceive) {
		t.Fatalf("pedido de vendedor sem conta aceito; err = %v", err)
	}
	vitrine, _ := ListProducts("", "")
	for _, p := range vitrine {
		if p.ID == produto {
			t.Fatal("livro de vendedor sem conta aparece na vitrine")
		}
	}
	p, err := GetProduct(produto)
	if err != nil || p.SellerReady {
		t.Fatalf("livro de vendedor sem conta marcado como pronto: %v", err)
	}
}

func TestComissaoConfiguravelComLimites(t *testing.T) {
	setupDB(t)
	if MarketplaceFeePercent() != 10 {
		t.Fatalf("padrão = %v; quer 10", MarketplaceFeePercent())
	}
	SetSetting("comissao_percentual", "15,5")
	if MarketplaceFeePercent() != 15.5 {
		t.Fatalf("comissão configurada = %v", MarketplaceFeePercent())
	}
	SetSetting("comissao_percentual", "90") // fora do limite: volta ao padrão
	if MarketplaceFeePercent() != 10 {
		t.Fatalf("comissão absurda aceita: %v", MarketplaceFeePercent())
	}
}
