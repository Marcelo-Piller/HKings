package models

import (
	"errors"
	"strings"
	"testing"

	"github.com/vendas/loja/database"
)

func TestNivelDependeSoDaMedia(t *testing.T) {
	casos := []struct {
		media float64
		qtd   int
		quer  string
	}{
		{0, 0, "cinza"}, // sem avaliação ainda não há média
		{2.9, 100, "cinza"},
		{3.0, 1, "bronze"},
		{3.8, 2, "prata"},
		{4.4, 3, "ouro"},
		{4.79, 500, "ouro"},
		{4.8, 1, "esmeralda"},
		{5, 1, "esmeralda"},
	}
	for _, c := range casos {
		if got := TierFor(c.media, c.qtd).Key; got != c.quer {
			t.Errorf("TierFor(%v, %d) = %s; quer %s", c.media, c.qtd, got, c.quer)
		}
	}
}

func TestEstrelasComMeiaEstrela(t *testing.T) {
	got := strings.Join(SellerRating{Average: 3.6}.Stars(), ",")
	if got != "cheia,cheia,cheia,meia,vazia" {
		t.Fatalf("3,6 estrelas = %s", got)
	}
	if got := (SellerRating{Average: 4.7}).AverageLabel(); got != "4,7" {
		t.Fatalf("AverageLabel = %q", got)
	}
}

func criaCliente(t *testing.T, email string) int {
	t.Helper()
	id, err := CreateCustomer("Leitor", email, "senha-do-leitor")
	if err != nil {
		t.Fatal(err)
	}
	return int(id)
}

// pedidoDoCliente cria um pedido do vendedor para o cliente, já no status pedido.
func pedidoDoCliente(t *testing.T, vendedor, cliente int, status string) int {
	t.Helper()
	o := pedidoCom(produtoDe(t, vendedor, 20))
	id, err := CreateOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	database.DB.Exec(`UPDATE orders SET customer_id=?, status=? WHERE id=?`, cliente, status, id)
	return int(id)
}

func TestAvaliacaoSoDoCompradorDepoisDoEnvio(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	vendedor := vendedorConectado(t, "ana", cpfTeste1, "111")
	comprador := criaCliente(t, "leitor@exemplo.com")
	outro := criaCliente(t, "outro@exemplo.com")

	pendente := pedidoDoCliente(t, vendedor, comprador, "pago")
	if err := CreateReview(pendente, comprador, 5, ""); !errors.Is(err, ErrReviewNotAllowed) {
		t.Fatalf("avaliou pedido ainda não enviado; err = %v", err)
	}

	enviado := pedidoDoCliente(t, vendedor, comprador, "enviado")
	if err := CreateReview(enviado, outro, 1, ""); !errors.Is(err, ErrReviewNotAllowed) {
		t.Fatalf("outro cliente avaliou pedido alheio; err = %v", err)
	}
	if err := CreateReview(enviado, comprador, 6, ""); !errors.Is(err, ErrReviewNotAllowed) {
		t.Fatalf("aceitou 6 estrelas; err = %v", err)
	}
	if err := CreateReview(enviado, comprador, 4, "  Chegou bem embalado  "); err != nil {
		t.Fatal(err)
	}
	if err := CreateReview(enviado, comprador, 1, ""); !errors.Is(err, ErrAlreadyReviewed) {
		t.Fatalf("avaliou o mesmo pedido duas vezes; err = %v", err)
	}

	r := GetSellerRating(vendedor)
	if r.Count != 1 || r.Average != 4 || r.Tier.Key != "prata" {
		t.Fatalf("reputação = %+v", r)
	}
	if rev := GetReviewForOrder(enviado); rev == nil || rev.Comment != "Chegou bem embalado" {
		t.Fatalf("avaliação gravada = %+v", rev)
	}
}

func TestVendaDoSiteNaoRecebeAvaliacao(t *testing.T) {
	setupDB(t)
	comprador := criaCliente(t, "leitor@exemplo.com")
	pedido := pedidoDoCliente(t, 0, comprador, "entregue")
	if err := CreateReview(pedido, comprador, 5, ""); !errors.Is(err, ErrReviewNotAllowed) {
		t.Fatalf("venda do próprio site avaliada; err = %v", err)
	}
}

func TestDenunciaValidaPedidoEMotivo(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")
	bia := vendedorConectado(t, "bia", cpfTeste2, "222")
	cliente := criaCliente(t, "leitor@exemplo.com")
	outro := criaCliente(t, "outro@exemplo.com")
	pedido := pedidoDoCliente(t, ana, cliente, "pago")
	texto := "Paguei há vinte dias e o vendedor parou de responder."

	if err := CreateReport(ana, cliente, 0, 0, "inventado", texto); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("motivo inventado aceito; err = %v", err)
	}
	if err := CreateReport(ana, cliente, 0, 0, "fraude", "curto"); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("descrição curta aceita; err = %v", err)
	}
	if err := CreateReport(bia, cliente, pedido, 0, "fraude", texto); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("pedido de outro vendedor aceito; err = %v", err)
	}
	if err := CreateReport(ana, outro, pedido, 0, "fraude", texto); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("pedido de outro cliente aceito; err = %v", err)
	}
	if err := CreateReport(ana, cliente, pedido, 0, "nao_entregue", texto); err != nil {
		t.Fatal(err)
	}

	if n := CountOpenReports(ana); n != 1 {
		t.Fatalf("denúncias abertas = %d", n)
	}
	lista, err := ListReports("aberta")
	if err != nil || len(lista) != 1 || lista[0].CustomerEmail != "leitor@exemplo.com" || lista[0].ReasonLabel() != "Paguei e não recebi" {
		t.Fatalf("ListReports = %+v, %v", lista, err)
	}
	if err := UpdateReport(lista[0].ID, "resolvida", "Reembolso feito"); err != nil {
		t.Fatal(err)
	}
	if n := CountOpenReports(0); n != 0 {
		t.Fatalf("denúncia resolvida ainda conta como aberta: %d", n)
	}
	if err := UpdateReport(lista[0].ID, "apagada", ""); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("status inventado aceito; err = %v", err)
	}
}

func TestSuspenderVendedorTiraLivrosDaLoja(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")
	livro := produtoDe(t, ana, 30)

	if err := SetSellerActive(ana, false); err != nil {
		t.Fatal(err)
	}
	vitrine, _ := ListProducts("", "")
	for _, p := range vitrine {
		if p.ID == livro {
			t.Fatal("livro de vendedor suspenso continua na vitrine")
		}
	}
	if _, err := CreateOrder(pedidoCom(livro)); err == nil {
		t.Fatal("pedido de vendedor suspenso aceito")
	}
	if _, err := AuthenticateSeller("ana", "senha-com-12-caracteres"); err == nil {
		t.Fatal("vendedor suspenso conseguiu entrar")
	}
}

func TestFotoDePerfilDevolveAnterior(t *testing.T) {
	setupDB(t)
	cliente := criaCliente(t, "leitor@exemplo.com")
	if _, err := SetCustomerAvatar(cliente, "/static/img/uploads/a.jpg"); err != nil {
		t.Fatal(err)
	}
	antiga, err := SetCustomerAvatar(cliente, "/static/img/uploads/b.jpg")
	if err != nil || antiga != "/static/img/uploads/a.jpg" {
		t.Fatalf("foto anterior = %q, %v", antiga, err)
	}
	c, _ := GetCustomerByID(cliente)
	if c.AvatarURL != "/static/img/uploads/b.jpg" {
		t.Fatalf("AvatarURL = %q", c.AvatarURL)
	}
}
