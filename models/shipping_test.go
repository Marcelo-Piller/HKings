package models

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vendas/loja/database"
)

func enderecoValido() Address {
	return Address{PostalCode: "20040-002", Street: "Rua  da Assembleia", Number: "10", District: "Centro", City: "Rio de Janeiro", State: "rj"}
}

func TestEnderecoParaEtiqueta(t *testing.T) {
	a := enderecoValido()
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	if a.PostalCode != "20040002" || a.State != "RJ" || a.Street != "Rua da Assembleia" {
		t.Fatalf("normalizado = %+v", a)
	}
	if got := a.String(); got != "Rua da Assembleia, 10 - Centro, Rio de Janeiro/RJ - CEP 20040-002" {
		t.Fatalf("String = %q", got)
	}
	for nome, estraga := range map[string]func(*Address){
		"sem número":   func(a *Address) { a.Number = " " },
		"CEP curto":    func(a *Address) { a.PostalCode = "2004" },
		"UF inventada": func(a *Address) { a.State = "XX" },
		"sem cidade":   func(a *Address) { a.City = "" },
	} {
		b := enderecoValido()
		estraga(&b)
		if err := b.Normalize(); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("%s aceito", nome)
		}
	}
}

func TestFreteEntraNoTotalMasNaoNaComissao(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	vendedor := vendedorConectado(t, "ana", cpfTeste1, "111")
	o := pedidoCom(produtoDe(t, vendedor, 100))
	o.Address = ""
	o.ShipTo = enderecoValido()
	o.Shipping = Shipping{Cents: 2550, ServiceID: 2, Service: "Correios SEDEX", Days: 3}
	o.CustomerDocument = "11144477735"
	id, err := CreateOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	if o.Total != 125.5 || o.MarketplaceFeeCents != 1000 {
		t.Fatalf("total %.2f, comissão %d; quer 125,50 e 10%% só dos livros (1000)", o.Total, o.MarketplaceFeeCents)
	}

	g, _ := GetOrder(int(id))
	if g.Shipping != o.Shipping || g.ShipTo.City != "Rio de Janeiro" || g.CustomerDocument != "11144477735" {
		t.Fatalf("frete/endereço gravados = %+v %+v", g.Shipping, g.ShipTo)
	}
	if !strings.Contains(g.Address, "CEP 20040-002") || g.ItemsTotal() != 100 {
		t.Fatalf("endereço %q, livros %.2f", g.Address, g.ItemsTotal())
	}
	// O pagamento precisa cobrir livros + frete.
	if _, err := MarkOrderPaid(int(id), 10000, "pix"); !errors.Is(err, ErrUnderpaid) {
		t.Fatalf("pagamento sem o frete liberou o pedido: %v", err)
	}
	if _, err := MarkOrderPaid(int(id), 12550, "pix"); err != nil {
		t.Fatal(err)
	}
	pago, _ := GetOrder(int(id))
	if !pago.CanGenerateLabel() {
		t.Fatal("pedido pago com frete do Melhor Envio deveria permitir etiqueta")
	}
	SetOrderLabel(pago.ID, "me-1", "https://x/etiqueta.pdf", "AA123456789BR")
	pago, _ = GetOrder(int(id))
	if pago.CanGenerateLabel() || pago.TrackingCode != "AA123456789BR" || !strings.HasSuffix(pago.TrackingURL(), "AA123456789BR") {
		t.Fatalf("depois da etiqueta: %+v", pago)
	}
}

func TestEnderecoInvalidoRecusaPedido(t *testing.T) {
	setupDB(t)
	o := pedidoCom(produtoDe(t, 0, 30))
	o.ShipTo = enderecoValido()
	o.ShipTo.Number = ""
	if _, err := CreateOrder(o); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("endereço sem número aceito: %v", err)
	}
}

func TestPedidoDigitalIgnoraFrete(t *testing.T) {
	setupDB(t)
	cliente := criaCliente(t, "leitora@exemplo.com")
	o := pedidoCom(ebookDe(t, 0, 20))
	o.CustomerID = cliente
	o.Shipping = Shipping{Cents: 999, ServiceID: 1, Service: "Correios PAC"}
	if _, err := CreateOrder(o); err != nil {
		t.Fatal(err)
	}
	if o.Total != 20 || o.Shipping.Cents != 0 {
		t.Fatalf("e-book cobrou frete: total %.2f frete %d", o.Total, o.Shipping.Cents)
	}
}

func TestPacotePadraoELimites(t *testing.T) {
	setupDB(t)
	p := Product{Name: "Livro", Price: 10, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: placeholderParaTeste}
	if err := validateProduct(&p); err != nil {
		t.Fatal(err)
	}
	if p.WeightG != DefaultBookWeightG || p.LengthCm != DefaultBookLength {
		t.Fatalf("pacote padrão não aplicado: %+v", p)
	}
	p.WeightG = 31000
	if err := validateProduct(&p); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("31 kg aceito: %v", err)
	}
}

func TestOrigemETokensDoMelhorEnvio(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	if o := GetShippingOrigin(5); o.Complete() || o.MEConnected {
		t.Fatalf("origem vazia = %+v", o)
	}
	if err := SaveShippingOrigin(5, enderecoValido(), "(21) 99999-0000"); err != nil {
		t.Fatal(err)
	}
	if err := SaveMETokens(5, "token-secreto", "refresh-secreto", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	o := GetShippingOrigin(5)
	if !o.Complete() || !o.MEConnected || o.City != "Rio de Janeiro" {
		t.Fatalf("origem = %+v", o)
	}
	var gravado string
	database.DB.QueryRow(`SELECT me_access_token FROM shipping_origins WHERE seller_id=5`).Scan(&gravado)
	if strings.Contains(gravado, "token-secreto") {
		t.Fatal("token do Melhor Envio gravado em texto puro")
	}
	acc, err := GetMEAccount(5)
	if err != nil || acc.AccessToken != "token-secreto" || acc.RefreshToken != "refresh-secreto" {
		t.Fatalf("conta = %+v, %v", acc, err)
	}
	// Salvar o endereço de novo não apaga a conexão, e desconectar não apaga o endereço.
	SaveShippingOrigin(5, enderecoValido(), "")
	if !GetShippingOrigin(5).MEConnected {
		t.Fatal("salvar endereço desconectou o Melhor Envio")
	}
	DisconnectME(5)
	if o := GetShippingOrigin(5); o.MEConnected || !o.Complete() {
		t.Fatalf("depois de desconectar: %+v", o)
	}
}
