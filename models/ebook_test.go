package models

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vendas/loja/database"
)

// epubMinimo monta um EPUB válido o bastante para os leitores: o primeiro
// arquivo do ZIP é "mimetype", sem compressão.
func epubMinimo(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("application/epub+zip"))
	w, _ = zw.Create("META-INF/container.xml")
	w.Write([]byte("<container/>"))
	zw.Close()
	return buf.Bytes()
}

func TestReconheceEbookPeloConteudo(t *testing.T) {
	if tipo, err := DetectEbookType([]byte("%PDF-1.7\n%âãÏÓ")); err != nil || tipo != EbookPDF {
		t.Fatalf("PDF: %q, %v", tipo, err)
	}
	if tipo, err := DetectEbookType(epubMinimo(t)); err != nil || tipo != EbookEPUB {
		t.Fatalf("EPUB: %q, %v", tipo, err)
	}

	// ZIP que não é EPUB é candidato a quadrinho (CBZ); sem imagens, ele é
	// recusado na contagem das páginas (TestQuadrinhoCBZ).
	var zipComum bytes.Buffer
	zw := zip.NewWriter(&zipComum)
	w, _ := zw.Create("leia-me.txt")
	w.Write([]byte("oi"))
	zw.Close()
	if tipo, err := DetectEbookType(zipComum.Bytes()); err != nil || tipo != EbookCBZ {
		t.Fatalf("ZIP: %q, %v", tipo, err)
	}
	if tipo, err := DetectEbookType([]byte("Rar!\x1a\x07\x01\x00resto")); err != nil || tipo != EbookCBR {
		t.Fatalf("RAR: %q, %v", tipo, err)
	}

	// Texto e executável renomeados não passam.
	for nome, conteudo := range map[string][]byte{
		"texto":      []byte("Capítulo 1 — era uma vez"),
		"executável": []byte("MZ\x90\x00\x03\x00\x00\x00"),
		"vazio":      {},
	} {
		if _, err := DetectEbookType(conteudo); !errors.Is(err, ErrInvalidEbook) {
			t.Errorf("%s aceito como e-book", nome)
		}
	}
}

func TestNomeDeArquivoDoEbookNaoSaiDaPasta(t *testing.T) {
	if !ValidEbookFileName("0123456789abcdef0123456789abcdef.epub") {
		t.Fatal("nome gerado recusado")
	}
	for _, nome := range []string{"", "../../etc/passwd", "livro.pdf", "0123456789abcdef0123456789abcdef.exe",
		"0123456789abcdef0123456789abcdef.pdf/../x", "/0123456789abcdef0123456789abcdef.pdf"} {
		if ValidEbookFileName(nome) {
			t.Errorf("aceitou %q", nome)
		}
	}
}

// ebookDe cadastra um e-book do vendedor (0 = do próprio site).
func ebookDe(t *testing.T, vendedor int, preco float64) int {
	t.Helper()
	res, err := database.DB.Exec(`INSERT INTO products (seller_id, name, price, stock, category, image_url, product_format, author, ebook_file, ebook_type, ebook_size)
		VALUES (?, 'Contos da Serra', ?, 0, 'Geral', ?, 'digital', 'Ana Autora', '0123456789abcdef0123456789abcdef.epub', 'epub', 2048)`,
		vendedor, preco, placeholderParaTeste)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

// pedidoPago cria e paga um pedido do cliente com os produtos.
func pedidoPago(t *testing.T, cliente int, endereco string, produtos ...int) *Order {
	t.Helper()
	o := pedidoCom(produtos...)
	o.CustomerID = cliente
	o.Address = endereco
	id, err := CreateOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MarkOrderPaid(int(id), OrderAmountCents(o), "pix"); err != nil {
		t.Fatal(err)
	}
	pago, _ := GetOrder(int(id))
	return pago
}

func TestEbookExigeArquivoENaoGuardaEstoque(t *testing.T) {
	setupDB(t)
	p := Product{Name: "Poemas", Price: 19.9, Stock: 50, Category: "Geral", Condition: ConditionNew,
		ImageURL: placeholderParaTeste, Format: FormatDigital}
	if err := validateProduct(&p); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("e-book sem arquivo aceito: %v", err)
	}
	p.EbookFile, p.EbookType = "0123456789abcdef0123456789abcdef.pdf", EbookPDF
	if err := validateProduct(&p); err != nil {
		t.Fatal(err)
	}
	if p.Stock != 0 || p.Condition != "" {
		t.Fatalf("e-book guardou estoque/estado: %d %q", p.Stock, p.Condition)
	}

	// Livro físico continua exigindo estado, e não carrega arquivo de e-book.
	f := Product{Name: "Físico", Price: 10, Stock: 2, Category: "Geral", ImageURL: placeholderParaTeste,
		EbookFile: "0123456789abcdef0123456789abcdef.pdf"}
	if err := validateProduct(&f); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("físico sem estado aceito: %v", err)
	}
	f.Condition = ConditionUsed
	if err := validateProduct(&f); err != nil || f.EbookFile != "" || f.Format != FormatPhysical {
		t.Fatalf("físico = %+v, %v", f, err)
	}
}

func TestFormatoNaoMudaDepoisDoCadastro(t *testing.T) {
	setupDB(t)
	id := ebookDe(t, 0, 20)
	virouFisico := Product{ID: id, Name: "Contos da Serra", Price: 20, Stock: 5, Category: "Geral",
		Condition: ConditionNew, ImageURL: placeholderParaTeste, Format: FormatPhysical, Active: true}
	if err := UpdateProduct(virouFisico); err == nil {
		t.Fatal("e-book virou livro físico")
	}
	p, _ := GetProduct(id)
	if !p.IsDigital() || p.EbookFile == "" {
		t.Fatalf("e-book perdeu o arquivo: %+v", p)
	}
}

func TestCompraDeEbookExigeContaENaoGastaEstoque(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	vendedor := vendedorConectado(t, "ana", cpfTeste1, "111")
	aprovarAutora(t, vendedor, "Ana Autora")
	livro := ebookDe(t, vendedor, 25)

	semConta := pedidoCom(livro)
	semConta.Address = ""
	if _, err := CreateOrder(semConta); !errors.Is(err, ErrDigitalNeedsAccount) {
		t.Fatalf("e-book comprado sem conta: %v", err)
	}

	cliente := criaCliente(t, "leitora@exemplo.com")
	o := pedidoCom(livro)
	o.CustomerID = cliente
	o.Address = ""
	o.Items[0].Quantity = 3
	id, err := CreateOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	if o.Items[0].Quantity != 1 || o.Total != 25 {
		t.Fatalf("quantidade %d, total %.2f; e-book é uma cópia só", o.Items[0].Quantity, o.Total)
	}
	if o.Address != DigitalDeliveryAddress {
		t.Fatalf("endereço = %q", o.Address)
	}
	var estoque int
	database.DB.QueryRow(`SELECT stock FROM products WHERE id=?`, livro).Scan(&estoque)
	if estoque != 0 {
		t.Fatalf("estoque do e-book mudou para %d", estoque)
	}

	// Antes de pagar, não há download.
	if _, err := GetEbookForCustomer(cliente, livro); !errors.Is(err, ErrEbookNotOwned) {
		t.Fatalf("download antes do pagamento: %v", err)
	}
	if _, err := MarkOrderPaid(int(id), 2500, "pix"); err != nil {
		t.Fatal(err)
	}
	pago, _ := GetOrder(int(id))
	if pago.Status != "entregue" || !pago.DigitalOnly() || !pago.IsPaid() {
		t.Fatalf("pedido só de e-book pago ficou %q", pago.Status)
	}
	d, err := GetEbookForCustomer(cliente, livro)
	if err != nil || d.Type != EbookEPUB {
		t.Fatalf("dono não baixa: %+v, %v", d, err)
	}

	// Outro cliente não baixa, e o dono não compra de novo.
	if _, err := GetEbookForCustomer(criaCliente(t, "outra@exemplo.com"), livro); !errors.Is(err, ErrEbookNotOwned) {
		t.Fatalf("quem não comprou baixou: %v", err)
	}
	denovo := pedidoCom(livro)
	denovo.CustomerID = cliente
	if _, err := CreateOrder(denovo); !errors.Is(err, ErrAlreadyOwned) {
		t.Fatalf("comprou o mesmo e-book duas vezes: %v", err)
	}
}

func TestPedidoMistoPedeEnderecoEFicaComoPago(t *testing.T) {
	setupDB(t)
	cliente := criaCliente(t, "leitora@exemplo.com")
	fisico := produtoDe(t, 0, 30)
	digital := ebookDe(t, 0, 15)

	semEndereco := pedidoCom(fisico, digital)
	semEndereco.CustomerID = cliente
	semEndereco.Address = "   "
	if _, err := CreateOrder(semEndereco); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("pedido com livro físico sem endereço aceito: %v", err)
	}

	pago := pedidoPago(t, cliente, "Rua das Flores, 10", fisico, digital)
	// Há livro para enviar: fica "pago" até o envio, mas o e-book já sai.
	if pago.Status != "pago" || pago.DigitalOnly() || !pago.HasDigital() {
		t.Fatalf("pedido misto ficou %q", pago.Status)
	}
	if _, err := GetEbookForCustomer(cliente, digital); err != nil {
		t.Fatalf("e-book do pedido misto bloqueado: %v", err)
	}
}

func TestBibliotecaListaSoEbooksPagos(t *testing.T) {
	setupDB(t)
	cliente := criaCliente(t, "leitora@exemplo.com")
	pedidoPago(t, cliente, "Rua 1", produtoDe(t, 0, 30)) // físico: fora da biblioteca
	digital := ebookDe(t, 0, 15)
	pedidoPago(t, cliente, "", digital)

	naoPago := ebookDe(t, 0, 9)
	o := pedidoCom(naoPago)
	o.CustomerID = cliente
	if _, err := CreateOrder(o); err != nil {
		t.Fatal(err)
	}

	itens, err := ListLibrary(cliente)
	if err != nil || len(itens) != 1 || itens[0].ProductID != digital || !itens[0].Available || itens[0].Author != "Ana Autora" {
		t.Fatalf("biblioteca = %+v, %v", itens, err)
	}

	// Livro tirado da loja continua na biblioteca de quem comprou.
	SetProductActive(digital, false)
	if itens, _ := ListLibrary(cliente); len(itens) != 1 {
		t.Fatal("livro fora da loja sumiu da biblioteca")
	}
}

func TestPrateleiraDeEbooksNaVitrine(t *testing.T) {
	setupDB(t)
	digital := ebookDe(t, 0, 15)
	ebooks, _ := ListProducts("", FormatDigital)
	for _, p := range ebooks {
		if !p.IsDigital() {
			t.Fatalf("livro físico %d na prateleira de e-books", p.ID)
		}
	}
	if len(ebooks) == 0 || ebooks[0].ID != digital {
		t.Fatalf("e-book fora da prateleira: %+v", ebooks)
	}
	novos, _ := ListProducts("", ConditionNew)
	for _, p := range novos {
		if p.IsDigital() {
			t.Fatal("e-book apareceu entre os livros novos")
		}
	}
	if !ValidShelfFilter(FormatDigital) || ValidCondition(FormatDigital) {
		t.Fatal("digital é prateleira, não estado de livro físico")
	}
}

func TestSacolaGuardaUmaCopiaDoEbook(t *testing.T) {
	ebook := Product{ID: 7, Name: "Contos", Price: 15, Format: FormatDigital}
	var c Cart
	c.Add(ebook, 3, "")
	c.Add(ebook, 2, "")
	c.UpdateQty(7, 9)
	if c.Count() != 1 || c.Total() != 15 {
		t.Fatalf("sacola com %d cópias do e-book", c.Count())
	}
	if !c.HasDigital() || c.HasPhysical() {
		t.Fatal("HasDigital/HasPhysical errados")
	}
}

func TestArquivoDoEbookNaoVaiParaOCookieDaSacola(t *testing.T) {
	c := Cart{Items: []CartItem{{Product: Product{ID: 1, Name: "Contos", Description: strings.Repeat("sinopse ", 300),
		Format: FormatDigital, EbookFile: "0123456789abcdef0123456789abcdef.pdf"}, Quantity: 1}}}
	data, _ := json.Marshal(c)
	if bytes.Contains(data, []byte("0123456789abcdef")) || bytes.Contains(data, []byte("sinopse")) {
		t.Fatalf("cookie da sacola leva arquivo ou sinopse: %s", data)
	}
	var volta Cart
	if err := json.Unmarshal(data, &volta); err != nil || !volta.Items[0].Product.IsDigital() {
		t.Fatalf("sacola perdeu o formato ao voltar do cookie: %+v, %v", volta, err)
	}
}
