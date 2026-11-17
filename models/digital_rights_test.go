package models

import (
	"errors"
	"strings"
	"testing"

	"github.com/vendas/loja/database"
)

const comprovacaoTeste = "ISBN 978-85-00000-00-0, registro na Biblioteca Nacional e perfil de autora no site."

// aprovarAutora pede e aprova a venda de e-books como autora com esse nome.
func aprovarAutora(t *testing.T, vendedor int, nome string) {
	t.Helper()
	if err := RequestDigitalRights(vendedor, DigitalKindAuthor, []string{nome}, comprovacaoTeste); err != nil {
		t.Fatal(err)
	}
	if err := ReviewDigitalRights(vendedor, true, []string{nome}, ""); err != nil {
		t.Fatal(err)
	}
}

func ebookNovo(vendedor int, autor, hash string) Product {
	return Product{SellerID: vendedor, Name: "Livro digital", Price: 20, Category: "Geral", ImageURL: placeholderParaTeste,
		Format: FormatDigital, Author: autor, EbookFile: "0123456789abcdef0123456789abcdef.pdf", EbookType: EbookPDF,
		EbookSHA256: hash, RightsDeclared: true, Active: true}
}

func TestEbookSoComAprovacao(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")

	if err := CreateProduct(ebookNovo(ana, "Ana Souza", "h1")); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("e-book sem aprovação aceito: %v", err)
	}
	// Livro físico continua livre.
	fisico := Product{SellerID: ana, Name: "Físico", Price: 10, Stock: 1, Category: "Geral", Condition: ConditionNew, ImageURL: placeholderParaTeste}
	if err := CreateProduct(fisico); err != nil {
		t.Fatalf("livro físico bloqueado: %v", err)
	}

	if err := RequestDigitalRights(ana, DigitalKindAuthor, []string{"Ana Souza"}, comprovacaoTeste); err != nil {
		t.Fatal(err)
	}
	if d := GetDigitalRights(ana); !d.Pending() || CountPendingDigital() != 1 {
		t.Fatalf("pedido = %+v", d)
	}
	if err := CreateProduct(ebookNovo(ana, "Ana Souza", "h1")); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("e-book aceito com pedido ainda em análise: %v", err)
	}
	if err := ReviewDigitalRights(ana, true, []string{"Ana Souza\nA. Souza"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := CreateProduct(ebookNovo(ana, "  ana   SOUZA ", "h1")); err != nil {
		t.Fatalf("autora aprovada barrada (nome com outra caixa/espaços): %v", err)
	}
}

func TestAutoraNaoVendeObraDeOutroAutor(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")
	aprovarAutora(t, ana, "Ana Souza")

	err := CreateProduct(ebookNovo(ana, "Mauricio de Sousa", "turma-da-monica"))
	if !errors.Is(err, ErrInvalidProduct) || !strings.Contains(err.Error(), "Ana Souza") {
		t.Fatalf("autora vendeu obra de outro autor: %v", err)
	}
	if err := CreateProduct(ebookNovo(ana, "", "sem-autor")); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("e-book sem autor aceito para autora: %v", err)
	}
}

func TestEditoraParceiraPublicaQualquerAutor(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	editora := vendedorConectado(t, "editora", cnpjTeste1, "111")
	if err := RequestDigitalRights(editora, DigitalKindPublisher, nil, "Contrato de distribuição digital com os autores do catálogo."); err != nil {
		t.Fatal(err)
	}
	if err := ReviewDigitalRights(editora, true, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := CreateProduct(ebookNovo(editora, "Qualquer Autor", "h-editora")); err != nil {
		t.Fatalf("editora parceira barrada: %v", err)
	}
	if GetDigitalRights(editora).BadgeLabel() != "Editora parceira" {
		t.Fatal("selo da editora errado")
	}
}

func TestMesmoArquivoNaoVendeDuasVezes(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")
	bia := vendedorConectado(t, "bia", cpfTeste2, "222")
	aprovarAutora(t, ana, "Autora Um")
	aprovarAutora(t, bia, "Autora Um") // mesmo nome, outra pessoa: o hash ainda barra

	if err := CreateProduct(ebookNovo(ana, "Autora Um", "pdf-pirata")); err != nil {
		t.Fatal(err)
	}
	err := CreateProduct(ebookNovo(bia, "Autora Um", "pdf-pirata"))
	if !errors.Is(err, ErrInvalidProduct) || !strings.Contains(err.Error(), "já está cadastrado") {
		t.Fatalf("mesmo arquivo posto à venda duas vezes: %v", err)
	}
	// Editar o próprio livro mantendo o arquivo não conta como duplicado.
	var id int
	database.DB.QueryRow(`SELECT id FROM products WHERE ebook_sha256='pdf-pirata'`).Scan(&id)
	p, _ := GetProduct(id)
	p.Name = "Título corrigido"
	if err := UpdateProductBySeller(*p, ana); err != nil {
		t.Fatalf("editar o próprio e-book barrado como duplicado: %v", err)
	}
}

func TestRevogarTiraEbooksDaLoja(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	ana := vendedorConectado(t, "ana", cpfTeste1, "111")
	aprovarAutora(t, ana, "Ana Autora")
	livro := ebookDe(t, ana, 30)

	if p, _ := GetProduct(livro); !p.SellerReady {
		t.Fatal("e-book de autora aprovada não está à venda")
	}
	if err := ReviewDigitalRights(ana, false, nil, ""); !errors.Is(err, ErrDigitalRequest) {
		t.Fatalf("revogar sem motivo aceito: %v", err)
	}
	if err := ReviewDigitalRights(ana, false, nil, "Obra denunciada como pirata."); err != nil {
		t.Fatal(err)
	}
	vitrine, _ := ListProducts("", "")
	for _, p := range vitrine {
		if p.ID == livro {
			t.Fatal("e-book continua na vitrine depois de revogar")
		}
	}
	if p, _ := GetProduct(livro); p.SellerReady {
		t.Fatal("e-book revogado ainda pode ir para a sacola")
	}
	o := pedidoCom(livro)
	o.CustomerID = criaCliente(t, "leitora@exemplo.com")
	if _, err := CreateOrder(o); !errors.Is(err, ErrDigitalUnavailable) {
		t.Fatalf("e-book revogado vendido: %v", err)
	}
	// Recusado pode pedir de novo; aprovado não sobrescreve o próprio pedido.
	if err := RequestDigitalRights(ana, DigitalKindAuthor, []string{"Ana Autora"}, comprovacaoTeste); err != nil {
		t.Fatalf("novo pedido após recusa barrado: %v", err)
	}
}

func TestLojaVendeEbookSemAprovacao(t *testing.T) {
	setupDB(t)
	if err := CreateProduct(ebookNovo(0, "Machado de Assis", "dominio-publico")); err != nil {
		t.Fatalf("e-book da própria loja barrado: %v", err)
	}
}
