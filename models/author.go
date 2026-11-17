package models

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/vendas/loja/database"
)

// Páginas de autor: cada nome que aparece no campo "autor" dos livros tem uma
// página com a biografia e todos os livros dele na loja, de qualquer vendedor.
// O livro sempre leva à página do autor, mesmo quando quem vende é uma editora
// ou outro detentor dos direitos.
//
// A biografia é escrita por quem tem os direitos digitais da obra aprovados
// (o próprio autor ou uma editora parceira) ou pelo admin.

// MaxAuthorBio limita a biografia.
const MaxAuthorBio = 5000

// ErrInvalidAuthor: nome de autor vazio ou biografia longa demais.
var ErrInvalidAuthor = errors.New("dados do autor inválidos")

// Author é a página de um autor.
type Author struct {
	Slug      string
	Name      string
	Bio       string
	PhotoURL  string
	Website   string
	UpdatedAt string
	// Books são os livros à venda com este autor; preenchido por GetAuthorPage.
	Books []Product
}

// AuthorSlug transforma o nome no endereço da página: "José de Alencar" vira
// "jose-de-alencar". Sem acentos e sem diferença de maiúsculas, para o mesmo
// autor escrito de jeitos diferentes cair na mesma página.
func AuthorSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// acento separado da letra: some
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// AuthorURL é o endereço da página do autor; vazio sem nome.
func AuthorURL(name string) string {
	slug := AuthorSlug(name)
	if slug == "" {
		return ""
	}
	return "/autor/" + slug
}

// AuthorURL é o link do autor do livro.
func (p Product) AuthorURL() string { return AuthorURL(p.Author) }

// authorRow lê a página gravada do autor (sem os livros).
func authorRow(slug string) (*Author, error) {
	a := &Author{Slug: slug}
	err := database.DB.QueryRow(`SELECT name, bio, photo_url, website, updated_at FROM authors WHERE slug=?`, slug).
		Scan(&a.Name, &a.Bio, &a.PhotoURL, &a.Website, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// GetAuthor devolve a página gravada do autor, ou uma página vazia com o
// nome como aparece nos livros.
func GetAuthor(name string) *Author {
	slug := AuthorSlug(name)
	if a, err := authorRow(slug); err == nil {
		return a
	}
	return &Author{Slug: slug, Name: strings.Join(strings.Fields(name), " ")}
}

// GetAuthorPage monta a página pública do autor: biografia e livros à venda.
// Devolve sql.ErrNoRows quando não há página nem livro com esse autor.
func GetAuthorPage(slug string) (*Author, error) {
	books, err := ListProducts("", "")
	if err != nil {
		return nil, err
	}
	var mine []Product
	for _, p := range books {
		if p.Author != "" && AuthorSlug(p.Author) == slug {
			mine = append(mine, p)
		}
	}
	a, err := authorRow(slug)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if len(mine) == 0 {
			return nil, sql.ErrNoRows
		}
		a = &Author{Slug: slug, Name: mine[0].Author}
	}
	a.Books = mine
	return a, nil
}

// SaveAuthor grava a página do autor.
func SaveAuthor(a Author) error {
	a.Name = strings.Join(strings.Fields(a.Name), " ")
	a.Slug = AuthorSlug(a.Name)
	a.Bio = strings.TrimSpace(strings.ReplaceAll(a.Bio, "\r\n", "\n"))
	a.Website = strings.TrimSpace(a.Website)
	if a.Slug == "" || len([]rune(a.Name)) > 120 || len([]rune(a.Bio)) > MaxAuthorBio || len(a.Website) > 300 {
		return ErrInvalidAuthor
	}
	if a.Website != "" && !strings.HasPrefix(a.Website, "https://") && !strings.HasPrefix(a.Website, "http://") {
		a.Website = "https://" + a.Website
	}
	_, err := database.DB.Exec(`INSERT INTO authors (slug, name, bio, photo_url, website, updated_at)
		VALUES (?,?,?,?,?, CURRENT_TIMESTAMP)
		ON CONFLICT(slug) DO UPDATE SET name=excluded.name, bio=excluded.bio, photo_url=excluded.photo_url,
			website=excluded.website, updated_at=CURRENT_TIMESTAMP`,
		a.Slug, a.Name, a.Bio, a.PhotoURL, a.Website)
	return err
}

// AuthorSummary é uma linha da lista de autores do admin.
type AuthorSummary struct {
	Slug    string
	Name    string
	Books   int
	HasPage bool
}

// ListAuthors lista todos os autores: os dos livros cadastrados e os que já
// têm página, em ordem alfabética.
func ListAuthors() ([]AuthorSummary, error) {
	byslug := map[string]*AuthorSummary{}
	rows, err := database.DB.Query(`SELECT author FROM products WHERE archived=0 AND author<>''`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		slug := AuthorSlug(name)
		if slug == "" {
			continue
		}
		if byslug[slug] == nil {
			byslug[slug] = &AuthorSummary{Slug: slug, Name: name}
		}
		byslug[slug].Books++
	}
	rows.Close()
	rows, err = database.DB.Query(`SELECT slug, name FROM authors`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug, name string
		if rows.Scan(&slug, &name) != nil {
			continue
		}
		if byslug[slug] == nil {
			byslug[slug] = &AuthorSummary{Slug: slug}
		}
		byslug[slug].Name, byslug[slug].HasPage = name, true
	}
	rows.Close()
	out := make([]AuthorSummary, 0, len(byslug))
	for _, a := range byslug {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return AuthorSlug(out[i].Name) < AuthorSlug(out[j].Name) })
	return out, nil
}

// SellerAuthors são os autores cuja página o vendedor pode editar: os nomes
// aprovados para ele nos direitos digitais. Editora parceira edita os autores
// dos livros que ela mesma publica.
func SellerAuthors(sellerID int) []string {
	d := GetDigitalRights(sellerID)
	if !d.Approved() {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.Join(strings.Fields(name), " ")
		if s := AuthorSlug(name); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, name)
		}
	}
	for _, a := range d.Authors {
		add(a)
	}
	if d.Kind == DigitalKindPublisher {
		if products, err := ListProductsBySeller(sellerID); err == nil {
			for _, p := range products {
				if p.Author != "" {
					add(p.Author)
				}
			}
		}
	}
	return out
}

// SellerCanEditAuthor diz se o vendedor pode escrever a página deste autor.
func SellerCanEditAuthor(sellerID int, name string) bool {
	slug := AuthorSlug(name)
	for _, a := range SellerAuthors(sellerID) {
		if AuthorSlug(a) == slug {
			return true
		}
	}
	return false
}
