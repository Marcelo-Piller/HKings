package models

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/vendas/loja/database"
)

func TestNormalizeISBN(t *testing.T) {
	casos := map[string]string{
		"9780306406157":     "9780306406157",
		"978-0-306-40615-7": "9780306406157",
		"0-306-40615-2":     "9780306406157", // ISBN-10 vira 13
		"080442957X":        "9780804429573",
	}
	for in, quer := range casos {
		if got, ok := NormalizeISBN(in); !ok || got != quer {
			t.Errorf("NormalizeISBN(%q) = %q, %v; quer %q", in, got, ok, quer)
		}
	}
	for _, in := range []string{"9780306406158", "0306406153", "123", "97803064061X7", "abc0306406152"} {
		if _, ok := NormalizeISBN(in); ok {
			t.Errorf("ISBN inválido aceito: %q", in)
		}
	}
}

func TestParsePriceBR(t *testing.T) {
	casos := map[string]float64{"39,90": 39.9, "R$ 1.234,56": 1234.56, "39.90": 39.9, "1,234.56": 1234.56, "50": 50, "1.234": 1234}
	for in, quer := range casos {
		if got, err := ParsePriceBR(in); err != nil || got != quer {
			t.Errorf("ParsePriceBR(%q) = %v, %v; quer %v", in, got, err, quer)
		}
	}
	for _, in := range []string{"", "abc", "-5", "0"} {
		if _, err := ParsePriceBR(in); err == nil {
			t.Errorf("preço inválido aceito: %q", in)
		}
	}
}

func TestLePlanilhaDoExcel(t *testing.T) {
	// Excel em português: ";" e acentos em Windows-1252, cabeçalhos com acento.
	texto := "ISBN;Título;Autor;Categoria;Estado;Preço;Quantidade;Capa\r\n" +
		"0-306-40615-2;;;Romance;novo;R$ 49,90;3;\r\n" +
		";Memórias do Cárcere;Graciliano Ramos;clássicos;Usado;18;;https://exemplo.com/c.jpg\r\n" +
		";;;;;;;\r\n" +
		"123;Livro;;Romance;novo;abc;x;\r\n"
	data, _ := charmap.Windows1252.NewEncoder().Bytes([]byte(texto))
	rows, err := ParseImportCSV(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("linhas = %d; quer 3 (a vazia é ignorada)", len(rows))
	}
	if rows[0].ISBN != "9780306406157" || rows[0].Price != 49.9 || rows[0].Stock != 3 || rows[0].Line != 2 {
		t.Fatalf("linha 2 = %+v", rows[0])
	}
	if rows[1].Title != "Memórias do Cárcere" || rows[1].Stock != 1 || rows[1].CoverURL != "https://exemplo.com/c.jpg" {
		t.Fatalf("linha 3 = %+v (estoque em branco vale 1)", rows[1])
	}
	if len(rows[2].ParseErrors) != 3 { // ISBN, preço e estoque
		t.Fatalf("erros da linha 5 = %v", rows[2].ParseErrors)
	}

	if _, err := ParseImportCSV([]byte("nome,autor\nx,y\n")); err == nil || !strings.Contains(err.Error(), "preco") {
		t.Fatalf("planilha sem preço aceita: %v", err)
	}
}

func TestValidaPlanilhaAntesDePublicar(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	sebo := vendedorConectado(t, "sebo", cpfTeste1, "111")
	outro := vendedorConectado(t, "outro", cpfTeste2, "222")
	jaTenho := produtoDe(t, sebo, 20)
	database.DB.Exec(`UPDATE products SET isbn='9780306406157' WHERE id=?`, jaTenho)
	dosOutros := produtoDe(t, outro, 20)

	csv := "id,isbn,titulo,categoria,estado,preco,estoque\n" +
		",9780306406157,,,,25,4\n" + // já cadastrado: atualiza
		",,Vidas Secas,CLASSICOS,seminovo,30,1\n" + // categoria sem acento, "seminovo" = usado
		",,Sem Categoria,Inexistente,novo,10,1\n" +
		",,,Romance,novo,10,1\n" +
		",9780804429573,Dois,Romance,novo,10,1\n" +
		",978-0-8044-2957-3,Repetido,Romance,novo,10,1\n" +
		"999999,,X,Romance,novo,10,1\n"
	rows, err := ParseImportCSV([]byte(csv + fmt.Sprintf("%d,,Y,Romance,novo,10,1\n", dosOutros)))
	if err != nil {
		t.Fatal(err)
	}
	ValidateImportRows(rows, sebo)

	if rows[0].UpdateID != jaTenho || !rows[0].Ready() {
		t.Fatalf("ISBN já cadastrado não virou atualização: %+v", rows[0])
	}
	if !rows[1].Ready() || rows[1].Category != "Clássicos" || rows[1].Condition != ConditionUsed {
		t.Fatalf("linha válida recusada: %+v", rows[1])
	}
	for i, quer := range map[int]string{2: "não existe", 3: "título em branco", 5: "repetido", 6: "não é um livro físico seu", 7: "não é um livro físico seu"} {
		if rows[i].Ready() || !strings.Contains(strings.Join(rows[i].Errors, "|"), quer) {
			t.Errorf("linha %d: erros %v; quer %q", rows[i].Line, rows[i].Errors, quer)
		}
	}
	if !rows[4].Ready() {
		t.Fatalf("primeira ocorrência do ISBN recusada: %v", rows[4].Errors)
	}

	// Publicar a linha válida cria o livro com a ficha.
	p := rows[1].Product(sebo, placeholderForValidation)
	if err := CreateProduct(p); err != nil {
		t.Fatal(err)
	}
	if err := UpdatePriceStock(rows[0].UpdateID, sebo, rows[0].Price, rows[0].Stock); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetProduct(jaTenho); got.Price != 25 || got.Stock != 4 {
		t.Fatalf("atualização = %v/%d", got.Price, got.Stock)
	}
	if err := UpdatePriceStock(dosOutros, sebo, 1, 1); err == nil {
		t.Fatal("atualizou livro de outro vendedor")
	}
}

func TestEdicaoEmMassa(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	sebo := vendedorConectado(t, "sebo", cpfTeste1, "111")
	outro := vendedorConectado(t, "outro", cpfTeste2, "222")
	a, b := produtoDe(t, sebo, 20), produtoDe(t, sebo, 30)
	deOutro := produtoDe(t, outro, 40)

	n, err := BulkUpdateProducts(sebo, []BulkChange{{ID: a, Price: 18.5, Stock: 7, Active: true}, {ID: b, Price: 30, Stock: 10, Active: false}})
	if err != nil || n != 2 {
		t.Fatalf("salvos = %d, %v", n, err)
	}
	if p, _ := GetProduct(a); p.Price != 18.5 || p.Stock != 7 {
		t.Fatalf("livro a = %v/%d", p.Price, p.Stock)
	}
	// Livro de outro vendedor: nada é gravado, nem os livros válidos.
	_, err = BulkUpdateProducts(sebo, []BulkChange{{ID: a, Price: 99, Stock: 1, Active: true}, {ID: deOutro, Price: 1, Stock: 1, Active: true}})
	if !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("aceitou livro de outro vendedor: %v", err)
	}
	if p, _ := GetProduct(a); p.Price != 18.5 {
		t.Fatal("gravou parte das alterações")
	}
	if _, err := BulkUpdateProducts(sebo, []BulkChange{{ID: a, Price: 0, Stock: 1, Active: true}}); err == nil {
		t.Fatal("preço zero aceito")
	}
}
