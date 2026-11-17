package models

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/vendas/loja/database"
)

func TestAuthorSlug(t *testing.T) {
	casos := map[string]string{
		"José de Alencar":        "jose-de-alencar",
		"  JOSÉ   de   alencar ": "jose-de-alencar",
		"Clarice Lispector":      "clarice-lispector",
		"Conceição Evaristo":     "conceicao-evaristo",
		"J. R. R. Tolkien":       "j-r-r-tolkien",
		"???":                    "",
	}
	for nome, quer := range casos {
		if got := AuthorSlug(nome); got != quer {
			t.Errorf("AuthorSlug(%q) = %q; quer %q", nome, got, quer)
		}
	}
}

func TestPaginaDoAutorJuntaLivrosDeTodosOsVendedores(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	editora := vendedorConectado(t, "editora", cpfTeste1, "111")
	sebo := vendedorConectado(t, "sebo", cpfTeste2, "222")
	a := produtoDe(t, editora, 30)
	b := produtoDe(t, sebo, 15)
	outro := produtoDe(t, sebo, 20)
	database.DB.Exec(`UPDATE products SET author='Conceição Evaristo' WHERE id=?`, a)
	database.DB.Exec(`UPDATE products SET author='conceicao  evaristo' WHERE id=?`, b)
	database.DB.Exec(`UPDATE products SET author='Outra Pessoa' WHERE id=?`, outro)

	page, err := GetAuthorPage("conceicao-evaristo")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Books) != 2 {
		t.Fatalf("livros = %d; quer 2 (da editora e do sebo)", len(page.Books))
	}
	if page.Bio != "" {
		t.Fatal("biografia inventada")
	}
	if _, err := GetAuthorPage("ninguem-escreveu"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("autor sem livro nem página: %v", err)
	}

	if err := SaveAuthor(Author{Name: "Conceição Evaristo", Bio: "Escritora mineira.", Website: "exemplo.com"}); err != nil {
		t.Fatal(err)
	}
	page, _ = GetAuthorPage("conceicao-evaristo")
	if page.Bio != "Escritora mineira." || page.Website != "https://exemplo.com" || len(page.Books) != 2 {
		t.Fatalf("página = %+v", page)
	}
	if err := SaveAuthor(Author{Name: "Conceição Evaristo", Bio: strings.Repeat("a", MaxAuthorBio+1)}); !errors.Is(err, ErrInvalidAuthor) {
		t.Fatalf("biografia gigante aceita: %v", err)
	}

	autores, _ := ListAuthors()
	if len(autores) != 2 || !autores[0].HasPage || autores[0].Books != 2 {
		t.Fatalf("lista de autores = %+v", autores)
	}
}

func TestQuemEditaAPaginaDoAutor(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	autora := vendedorConectado(t, "autora", cpfTeste1, "111")
	sebo := vendedorConectado(t, "sebo", cpfTeste2, "222")
	aprovarAutora(t, autora, "Helena Duarte")

	if !SellerCanEditAuthor(autora, "helena  DUARTE") {
		t.Fatal("autora aprovada não edita a própria página")
	}
	if SellerCanEditAuthor(autora, "Machado de Assis") {
		t.Fatal("autora edita página de outro autor")
	}
	// Sebo vende livro físico da autora, mas não tem os direitos: não edita.
	livro := produtoDe(t, sebo, 10)
	database.DB.Exec(`UPDATE products SET author='Helena Duarte' WHERE id=?`, livro)
	if SellerCanEditAuthor(sebo, "Helena Duarte") {
		t.Fatal("vendedor sem direitos edita a página do autor")
	}
}
